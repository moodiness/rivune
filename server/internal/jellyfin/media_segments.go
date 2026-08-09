package jellyfin

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	minimumTrickplayWidth = 1
	maximumTrickplayWidth = 1024
	maximumTrickplayIndex = 1_000_000
	defaultTrickplayWidth = 320
	maximumMarkerSeconds  = 24 * 60 * 60
)

var jellyfinMediaSegmentTypes = map[string]string{
	"unknown":    "Unknown",
	"commercial": "Commercial",
	"preview":    "Preview",
	"recap":      "Recap",
	"outro":      "Outro",
	"intro":      "Intro",
}

func (handler *Handler) handleMediaSegments(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok {
		return
	}
	requestedTypes, ok := handler.parseMediaSegmentQuery(response, request.URL.Query())
	if !ok {
		return
	}
	itemID, err := ParseItemID(request.PathValue("itemId"))
	if err != nil {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return
	}
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID.String())
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	if item.MediaType != "episode" || item.SeriesID == "" || item.ParentOrdinal == nil || item.Ordinal == nil ||
		*item.ParentOrdinal < 1 || *item.Ordinal < 1 || handler.mediaSegments == nil {
		handler.writeMediaSegmentsUnavailable(response)
		return
	}
	seriesID, err := ParseItemID(item.SeriesID)
	if err != nil {
		handler.writeMediaSegmentsUnavailable(response)
		return
	}
	series, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, seriesID.String())
	if err != nil {
		if errors.Is(err, watchstate.ErrNotFound) {
			handler.writeMediaSegmentsUnavailable(response)
			return
		}
		handler.writeCatalogError(response, err)
		return
	}
	imdbProvider, imdbID, validIMDB := validatedProviderID("imdb", series.ProviderIDs["imdb"])
	if series.MediaType != "series" || !validIMDB || imdbProvider != "imdb" || len(imdbID) < 9 || len(imdbID) > 10 {
		handler.writeMediaSegmentsUnavailable(response)
		return
	}
	markers, err := handler.mediaSegments.Markers(request.Context(), session.Principal, playback.MarkerInput{
		IMDBID: imdbID, Season: *item.ParentOrdinal, Episode: *item.Ordinal,
		IncludeIntro: true, IncludeRecap: true, IncludeOutro: true,
	})
	if err != nil {
		handler.writeMediaSegmentError(response, err)
		return
	}

	known := make([]MediaSegmentDto, 0, len(markers.Markers))
	for _, marker := range markers.Markers {
		segmentType, supported := jellyfinSegmentType(marker.Type)
		startTicks, validStart := mediaMarkerTicks(marker.StartSeconds)
		endTicks, validEnd := mediaMarkerTicks(marker.EndSeconds)
		if !supported || !validStart || !validEnd || endTicks <= startTicks {
			continue
		}
		known = append(known, MediaSegmentDto{
			Id:     handler.catalogFacetID("media-segment", itemID.String()+"\x00"+segmentType+"\x00"+strconv.FormatInt(startTicks, 10)+"\x00"+strconv.FormatInt(endTicks, 10)),
			ItemId: itemID.String(), Type: segmentType, StartTicks: startTicks, EndTicks: endTicks,
		})
	}
	if len(known) == 0 {
		handler.writeMediaSegmentsUnavailable(response)
		return
	}

	items := make([]MediaSegmentDto, 0, len(known))
	for _, segment := range known {
		if len(requestedTypes) == 0 || requestedTypes[segment.Type] {
			items = append(items, segment)
		}
	}
	handler.writeJSON(response, http.StatusOK, QueryResult[MediaSegmentDto]{Items: items, TotalRecordCount: len(items), StartIndex: 0})
}

func (handler *Handler) handleTrickplayImage(response http.ResponseWriter, request *http.Request) {
	session, ok := handler.catalogSession(response, request)
	if !ok {
		return
	}
	if !validateCompatQueryNames(request.URL.Query(), "mediaSourceId") {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	mediaSourceID, err := boundedString(request.URL.Query(), "mediaSourceId", MaximumQueryValueBytes)
	if err != nil || mediaSourceID != "" && !validCompatUUID(mediaSourceID) {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return
	}
	itemID, err := ParseItemID(request.PathValue("itemId"))
	if err != nil || !validTrickplayPathValue(request.PathValue("width"), minimumTrickplayWidth, maximumTrickplayWidth) ||
		!validTrickplayPathValue(request.PathValue("index"), 0, maximumTrickplayIndex) {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return
	}
	if mediaSourceID != "" {
		canonicalSourceID, validSourceID := canonicalCompatUUID(mediaSourceID)
		if !validSourceID || canonicalSourceID != itemID.String() {
			handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
			return
		}
	}
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID.String())
	if err != nil {
		handler.writeCatalogError(response, err)
		return
	}
	if item.MediaType != "movie" && item.MediaType != "episode" {
		handler.writeCompatError(response, http.StatusNotFound, "ResourceNotFound", "Resource not found")
		return
	}
	delivery, supported := handler.playback.(trickplayDelivery)
	if !supported || !delivery.TrickplayAvailable() {
		handler.writeCompatError(response, http.StatusNotFound, "TrickplayUnavailable", "Trickplay image is unavailable")
		return
	}
	options, release, err := handler.playbackOptions(request.Context(), session, item, playback.Capabilities{
		StreamingProtocols: []string{"http", "hls"},
		ProcessingModes:    []string{"remux"},
	}, true)
	if err != nil {
		handler.writeTrickplayError(response, request, err)
		return
	}
	defer release()
	width, _ := strconv.Atoi(request.PathValue("width"))
	index, _ := strconv.Atoi(request.PathValue("index"))
	image, err := delivery.Trickplay(request.Context(), session.Principal, playback.TrickplayInput{
		SourceRef: options[0].SourceRef,
		TitleID:   item.ID,
		Width:     width,
		Index:     index,
	})
	if err != nil {
		handler.writeTrickplayError(response, request, err)
		return
	}
	digest := sha256.Sum256(image.JPEG)
	response.Header().Set("Cache-Control", "private, no-cache")
	response.Header().Set("Content-Type", "image/jpeg")
	response.Header().Set("ETag", fmt.Sprintf("\"%x\"", digest))
	response.Header().Set("Vary", "Authorization, X-Emby-Authorization, X-Emby-Token")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(response, request, strconv.Itoa(index)+".jpg", image.LastModified.UTC(), bytes.NewReader(image.JPEG))
}

func (handler *Handler) writeTrickplayError(response http.ResponseWriter, request *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, "InternalServerError", "Internal server error"
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired), errors.Is(err, playback.ErrForbidden):
		status, code, message = http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required"
	case errors.Is(err, playback.ErrInvalidInput):
		status, code, message = http.StatusBadRequest, "BadRequest", "Invalid trickplay request"
	case errors.Is(err, playback.ErrSourceReferenceExpired), errors.Is(err, playback.ErrNoPlayableSource), errors.Is(err, playback.ErrUnsupportedSource):
		status, code, message = http.StatusNotFound, "TrickplayUnavailable", "Trickplay image is unavailable"
	case errors.Is(err, playback.ErrMediaCapacityReached), errors.Is(err, playback.ErrMediaStorageLimit):
		status, code, message = http.StatusServiceUnavailable, "ServerBusy", "Trickplay generation is temporarily unavailable"
	case errors.Is(err, playback.ErrProviderUnavailable), errors.Is(err, playback.ErrMediaSourceFailed), errors.Is(err, playback.ErrMediaProcessingFailed):
		status, code, message = http.StatusBadGateway, "MediaSourceFailed", "Trickplay generation failed"
	}
	if request.Method == http.MethodHead {
		response.WriteHeader(status)
		return
	}
	handler.writeCompatError(response, status, code, message)
}

func jellyfinTrickplayMetadata(item BaseItemDto, mediaSourceID string) map[string]map[int]TrickplayInfoDto {
	if item.RunTimeTicks == nil || *item.RunTimeTicks <= 0 || mediaSourceID == "" {
		return nil
	}
	intervalTicks := int64(playback.TrickplayIntervalSeconds) * TicksPerSecond
	thumbnailCount := int((*item.RunTimeTicks-1)/intervalTicks + 1)
	maximumThumbnailCount := (maximumTrickplayIndex + 1) * playback.TrickplayTileColumns * playback.TrickplayTileRows
	if thumbnailCount < 1 || thumbnailCount > maximumThumbnailCount {
		return nil
	}
	return map[string]map[int]TrickplayInfoDto{
		mediaSourceID: {
			defaultTrickplayWidth: {
				Width: defaultTrickplayWidth, Height: playback.TrickplayThumbnailHeight(defaultTrickplayWidth),
				TileWidth: playback.TrickplayTileColumns, TileHeight: playback.TrickplayTileRows,
				ThumbnailCount: thumbnailCount, Interval: playback.TrickplayIntervalSeconds * 1000,
			},
		},
	}
}

func (handler *Handler) parseMediaSegmentQuery(response http.ResponseWriter, values url.Values) (map[string]bool, bool) {
	if !validateCompatQueryNames(values, "includeSegmentTypes") {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return nil, false
	}
	rawTypes, err := commaSeparated(values, "includeSegmentTypes")
	if err != nil || len(rawTypes) > len(jellyfinMediaSegmentTypes) {
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
		return nil, false
	}
	requested := make(map[string]bool, len(rawTypes))
	for _, rawType := range rawTypes {
		segmentType, valid := jellyfinMediaSegmentTypes[strings.ToLower(rawType)]
		if !valid {
			handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid query")
			return nil, false
		}
		requested[segmentType] = true
	}
	return requested, true
}

func validateCompatQueryNames(values url.Values, allowed ...string) bool {
	if err := validateQueryBudget(values); err != nil {
		return false
	}
	credentials := 0
	for name, entries := range values {
		credential := strings.EqualFold(name, "ApiKey") || strings.EqualFold(name, "api_key")
		valid := credential
		if credential {
			credentials += len(entries)
		}
		for _, candidate := range allowed {
			valid = valid || strings.EqualFold(name, candidate)
		}
		if !valid {
			return false
		}
	}
	return credentials <= 1
}

func validTrickplayPathValue(raw string, minimum, maximum int) bool {
	value, err := strconv.Atoi(raw)
	return err == nil && value >= minimum && value <= maximum && strconv.Itoa(value) == raw
}

func jellyfinSegmentType(markerType playback.MarkerType) (string, bool) {
	switch markerType {
	case playback.MarkerTypeIntro:
		return "Intro", true
	case playback.MarkerTypeRecap:
		return "Recap", true
	case playback.MarkerTypeOutro:
		return "Outro", true
	default:
		return "", false
	}
}

func mediaMarkerTicks(seconds float64) (int64, bool) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > maximumMarkerSeconds {
		return 0, false
	}
	return int64(math.Round(seconds * float64(TicksPerSecond))), true
}

func (handler *Handler) writeMediaSegmentsUnavailable(response http.ResponseWriter) {
	handler.writeCompatError(response, http.StatusNotFound, "MediaSegmentsUnavailable", "Media segment data is unavailable")
}

func (handler *Handler) writeMediaSegmentError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, playback.ErrActiveProfileRequired), errors.Is(err, playback.ErrForbidden):
		handler.writeCompatError(response, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
	case errors.Is(err, playback.ErrInvalidInput):
		handler.writeCompatError(response, http.StatusBadRequest, "BadRequest", "Invalid media segment request")
	case errors.Is(err, playback.ErrProviderUnavailable):
		handler.writeCompatError(response, http.StatusBadGateway, "ProviderUnavailable", "Media segment provider is unavailable")
	default:
		handler.writeCompatError(response, http.StatusInternalServerError, "InternalServerError", "Internal server error")
	}
}
