package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	maximumPlaybackInfoBodyBytes   = 256 << 10
	maximumRawDeviceProfileEntries = 256
	// Keep one admission beyond the 256-session registry below the 4096-reference global store limit.
	maximumCompatibilityMediaSources  = 15
	maximumCompatibilityMediaStreams  = 64
	maximumCompatibilitySubtitleIndex = 2047
	maximumCompatClientBitrate        = int64(1<<31 - 1)
	maximumCompatPlaybackBitrate      = int64(200_000_000)
)

const compatPlaybackInfoSourceOpenFailedMessage = "jellyfin compatibility playback info source open failed"

func (handler *Handler) handlePlaybackInfo(response http.ResponseWriter, request *http.Request) {
	if handler.playback == nil || handler.catalog == nil || handler.playSessions == nil {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	if userID := strings.TrimSpace(request.PathValue("userId")); userID != "" && !sameCompatUUID(userID, session.ProfileID) {
		handler.writeCompatError(response, http.StatusNotFound, "ItemNotFound", "The item was not found")
		return
	}
	input, err := parsePlaybackInfoRequest(response, request)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The playback request is invalid")
		return
	}
	if input.UserId != "" && !sameCompatUUID(input.UserId, session.ProfileID) {
		handler.writeCompatError(response, http.StatusNotFound, "ItemNotFound", "The item was not found")
		return
	}
	if canonicalMediaID, valid := canonicalCompatUUID(input.MediaSourceId); valid {
		input.MediaSourceId = canonicalMediaID
	}
	itemID, idErr := ParseItemID(request.PathValue("id"))
	if idErr != nil {
		handler.writeCompatError(response, http.StatusNotFound, "ItemNotFound", "The item was not found")
		return
	}
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID.String())
	if err != nil {
		handler.writePlaybackError(response, request, err)
		return
	}
	if item.ID == "" || item.MediaType != "movie" && item.MediaType != "episode" {
		handler.writeCompatError(response, http.StatusNotFound, "ItemNotFound", "The item was not found")
		return
	}
	capabilities, allowTranscode, err := handler.effectivePlaybackCapabilities(session, input)
	if err != nil {
		handler.writeCompatError(response, http.StatusUnprocessableEntity, "PlaybackProfileUnsupported", "The device playback profile is unsupported")
		return
	}
	if input.MediaSourceId != "" {
		playID, descriptors, reused := handler.playSessions.reuseCandidate(session, item.ID, input.MediaSourceId, capabilities, allowTranscode, nil)
		if reused {
			if err := handler.playSessions.setPlaybackPreferences(session, item.ID, playID, input.MediaSourceId, input.AudioStreamIndex, input.SubtitleStreamIndex); err != nil {
				handler.writePlaybackError(response, request, err)
				return
			}
			result := PlaybackInfoResponse{PlaySessionId: playID, MediaSources: make([]MediaSourceInfo, 0, len(descriptors))}
			for _, descriptor := range descriptors {
				result.MediaSources = append(result.MediaSources, mediaSourceDTO(item, playID, descriptor, capabilities, allowTranscode, input.StartTimeTicks))
			}
			binding, native, openErr := handler.openPlaybackInfoSourceWithPreferenceRetry(request.Context(), session, item.ID, playID, input.MediaSourceId, descriptors, input.StartTimeTicks, allowTranscode, input.AudioStreamIndex, input.SubtitleStreamIndex)
			if openErr != nil {
				handler.logPlaybackInfoSourceOpenFailure(input, capabilities, allowTranscode, "reuse_cached", openErr)
				handler.writePlaybackError(response, request, openErr)
				return
			}
			applyResolvedMediaSource(result.MediaSources, item.ID, binding.MediaSourceID, playID, input.StartTimeTicks, native)
			promoteSelectedMediaSource(result.MediaSources, binding.MediaSourceID)
			handler.writeJSON(response, http.StatusOK, result)
			return
		}
	}
	options, releaseOptions, err := handler.playbackOptions(request.Context(), session, item, capabilities, allowTranscode)
	if err != nil {
		handler.writePlaybackError(response, request, err)
		return
	}
	defer releaseOptions()
	if input.MediaSourceId != "" {
		playID, descriptors, reused := handler.playSessions.reuseCandidate(session, item.ID, input.MediaSourceId, capabilities, allowTranscode, options)
		if reused {
			if err := handler.playSessions.setPlaybackPreferences(session, item.ID, playID, input.MediaSourceId, input.AudioStreamIndex, input.SubtitleStreamIndex); err != nil {
				handler.writePlaybackError(response, request, err)
				return
			}
			result := PlaybackInfoResponse{PlaySessionId: playID, MediaSources: make([]MediaSourceInfo, 0, len(descriptors))}
			for _, descriptor := range descriptors {
				result.MediaSources = append(result.MediaSources, mediaSourceDTO(item, playID, descriptor, capabilities, allowTranscode, input.StartTimeTicks))
			}
			binding, native, openErr := handler.openPlaybackInfoSourceWithPreferenceRetry(request.Context(), session, item.ID, playID, input.MediaSourceId, descriptors, input.StartTimeTicks, allowTranscode, input.AudioStreamIndex, input.SubtitleStreamIndex)
			if openErr != nil {
				handler.logPlaybackInfoSourceOpenFailure(input, capabilities, allowTranscode, "reuse_refreshed", openErr)
				handler.writePlaybackError(response, request, openErr)
				return
			}
			applyResolvedMediaSource(result.MediaSources, item.ID, binding.MediaSourceID, playID, input.StartTimeTicks, native)
			promoteSelectedMediaSource(result.MediaSources, binding.MediaSourceID)
			handler.writeJSON(response, http.StatusOK, result)
			return
		}
		if input.MediaSourceId != item.ID || handler.playSessions.candidateExists(session, item.ID, input.MediaSourceId) {
			handler.writeCompatError(response, http.StatusNotFound, "PlaybackSessionNotFound", "The playback session or source is invalid or expired")
			return
		}
	}
	result, err := handler.registerPlaybackInfo(request.Context(), session, item, capabilities, allowTranscode, options, input.MediaSourceId, input.StartTimeTicks, input.AudioStreamIndex, input.SubtitleStreamIndex)
	if err != nil {
		handler.writePlaybackError(response, request, err)
		return
	}
	playID := result.PlaySessionId
	mediaID := input.MediaSourceId
	if mediaID == "" && len(result.MediaSources) != 0 {
		mediaID = result.MediaSources[0].Id
	}
	if mediaID != "" {
		descriptors := make([]playSourceDescriptor, 0, len(result.MediaSources))
		for _, source := range result.MediaSources {
			descriptors = append(descriptors, playSourceDescriptor{ID: source.Id})
		}
		binding, native, openErr := handler.openPlaybackInfoSourceWithPreferenceRetry(request.Context(), session, item.ID, playID, mediaID, descriptors, input.StartTimeTicks, allowTranscode, input.AudioStreamIndex, input.SubtitleStreamIndex)
		if openErr != nil {
			handler.logPlaybackInfoSourceOpenFailure(input, capabilities, allowTranscode, "registered", openErr)
			_ = handler.playSessions.close(context.WithoutCancel(request.Context()), session, item.ID, playID, result.MediaSources[0].Id)
			handler.writePlaybackError(response, request, openErr)
			return
		}
		applyResolvedMediaSource(result.MediaSources, item.ID, binding.MediaSourceID, playID, input.StartTimeTicks, native)
		promoteSelectedMediaSource(result.MediaSources, binding.MediaSourceID)
	}
	handler.writeJSON(response, http.StatusOK, result)
}

func (handler *Handler) openPlaybackInfoSourceWithPreferenceRetry(ctx context.Context, session AuthenticatedSession, itemID, playID, requestedMediaID string, descriptors []playSourceDescriptor, startTimeTicks int64, allowTranscode bool, audioIndex, subtitleIndex *int) (playSessionBinding, playback.Session, error) {
	binding, native, onlyTrackPreferenceFailures, err := handler.openPlaybackInfoSource(ctx, session, itemID, playID, requestedMediaID, descriptors, startTimeTicks)
	if err == nil || allowTranscode || audioIndex == nil && subtitleIndex == nil || !onlyTrackPreferenceFailures {
		return binding, native, err
	}
	subtitlesOff := -1
	if preferenceErr := handler.playSessions.setPlaybackPreferences(session, itemID, playID, requestedMediaID, nil, &subtitlesOff); preferenceErr != nil {
		return playSessionBinding{}, playback.Session{}, preferenceErr
	}
	binding, native, _, err = handler.openPlaybackInfoSource(ctx, session, itemID, playID, requestedMediaID, descriptors, startTimeTicks)
	return binding, native, err
}

func (handler *Handler) openPlaybackInfoSource(ctx context.Context, session AuthenticatedSession, itemID, playID, requestedMediaID string, descriptors []playSourceDescriptor, startTimeTicks int64) (playSessionBinding, playback.Session, bool, error) {
	candidates := make([]string, 0, len(descriptors))
	seen := make(map[string]struct{}, len(descriptors))
	appendCandidate := func(mediaID string) {
		if mediaID == "" {
			return
		}
		if _, exists := seen[mediaID]; exists {
			return
		}
		seen[mediaID] = struct{}{}
		candidates = append(candidates, mediaID)
	}
	appendCandidate(requestedMediaID)
	for _, descriptor := range descriptors {
		appendCandidate(descriptor.ID)
	}
	if len(candidates) == 0 {
		return playSessionBinding{}, playback.Session{}, false, playback.ErrNoPlayableSource
	}
	var lastErr error
	onlyTrackPreferenceFailures := true
	for _, mediaID := range candidates {
		binding, _, native, err := handler.playSessions.openAndTouch(ctx, session, itemID, playID, mediaID, startTimeTicks)
		if err == nil {
			return binding, native, false, nil
		}
		lastErr = err
		if !errors.Is(err, playback.ErrTranscodingDisabled) && !errors.Is(err, playback.ErrClientCapabilityMissing) {
			onlyTrackPreferenceFailures = false
		}
		if !playbackSourceRetryable(err) {
			return playSessionBinding{}, playback.Session{}, false, err
		}
	}
	return playSessionBinding{}, playback.Session{}, onlyTrackPreferenceFailures, lastErr
}

func playbackSourceRetryable(err error) bool {
	if err == nil || errors.Is(err, playback.ErrActiveProfileRequired) ||
		errors.Is(err, playback.ErrForbidden) || errors.Is(err, playback.ErrSessionNotFound) || errors.Is(err, errPlaySessionNotFound) ||
		errors.Is(err, playback.ErrMediaCapacityReached) || errors.Is(err, playback.ErrMediaStorageLimit) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return errors.Is(err, playback.ErrInvalidInput) ||
		errors.Is(err, playback.ErrUnsupportedSource) ||
		errors.Is(err, playback.ErrTranscodingDisabled) ||
		errors.Is(err, playback.ErrClientCapabilityMissing) ||
		errors.Is(err, playback.ErrProviderUnavailable) ||
		errors.Is(err, playback.ErrMediaSourceFailed) ||
		errors.Is(err, playback.ErrSourceReferenceExpired) ||
		errors.Is(err, playback.ErrNoPlayableSource)
}

func (handler *Handler) logPlaybackInfoSourceOpenFailure(input PlaybackInfoRequest, capabilities playback.Capabilities, allowTranscode bool, stage string, err error) {
	if handler == nil || handler.logger == nil {
		return
	}
	handler.logger.LogAttrs(
		context.Background(),
		slog.LevelInfo,
		compatPlaybackInfoSourceOpenFailedMessage,
		slog.String("stage", stage),
		slog.String("error_class", playbackInfoSourceErrorClass(err)),
		slog.Bool("enable_direct_play", playbackFlag(input.EnableDirectPlay, true)),
		slog.Bool("enable_direct_stream", playbackFlag(input.EnableDirectStream, true)),
		slog.Bool("enable_transcoding", playbackFlag(input.EnableTranscoding, true)),
		slog.Bool("allow_transcode", allowTranscode),
		slog.Int("protocols", len(capabilities.StreamingProtocols)),
		slog.Int("containers", len(capabilities.Containers)),
		slog.Int("codecs", len(capabilities.VideoCodecs)+len(capabilities.AudioCodecs)),
		slog.Int("profiles", mediaProfileDiagnosticCount(capabilities.MediaProfiles)),
		slog.Int("modes", len(capabilities.ProcessingModes)+len(capabilities.SubtitleModes)),
	)
}

func mediaProfileDiagnosticCount(profiles []playback.MediaProfile) int {
	unique := make(map[string]struct{})
	for _, profile := range profiles {
		containers := profile.ContainersCSV
		if containers == "" {
			containers = profile.Container
		}
		audios := profile.AudioCodecsCSV
		if audios == "" {
			audios = profile.AudioCodec
		}
		containerValues := strings.Split(containers, ",")
		audioValues := strings.Split(audios, ",")
		for _, container := range containerValues {
			container = normalizedContainer(container)
			if container == "" || normalizeCapability(profile.VideoCodec) == "" {
				continue
			}
			for _, audio := range audioValues {
				key := container + "\x00" + normalizeCapability(profile.VideoCodec) + "\x00" + normalizeCapability(audio)
				unique[key] = struct{}{}
			}
		}
	}
	return len(unique)
}

func playbackInfoSourceErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, playback.ErrActiveProfileRequired):
		return "profile_required"
	case errors.Is(err, playback.ErrForbidden):
		return "forbidden"
	case errors.Is(err, errPlaySessionNotFound), errors.Is(err, playback.ErrSessionNotFound):
		return "session_not_found"
	case errors.Is(err, playback.ErrSourceReferenceExpired):
		return "source_reference_expired"
	case errors.Is(err, playback.ErrNoPlayableSource):
		return "no_playable_source"
	case errors.Is(err, playback.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, playback.ErrUnsupportedSource):
		return "unsupported_source"
	case errors.Is(err, playback.ErrTranscodingDisabled):
		return "transcoding_disabled"
	case errors.Is(err, playback.ErrClientCapabilityMissing):
		return "client_capability_missing"
	case errors.Is(err, playback.ErrProviderUnavailable):
		return "provider_unavailable"
	case errors.Is(err, playback.ErrMediaSourceFailed):
		return "media_source_failed"
	case errors.Is(err, playback.ErrMediaCapacityReached):
		return "media_capacity_reached"
	case errors.Is(err, playback.ErrMediaStorageLimit):
		return "media_storage_limit"
	default:
		return "internal"
	}
}

func (handler *Handler) cachedDetailMediaSources(session AuthenticatedSession, item watchstate.CatalogTitle) []MediaSourceInfo {
	if handler == nil || handler.playSessions == nil || item.ID == "" || item.MediaType != "movie" && item.MediaType != "episode" {
		return nil
	}
	capabilities, allowTranscode, err := handler.effectivePlaybackCapabilities(session, PlaybackInfoRequest{})
	if err != nil {
		return nil
	}
	playID, descriptors, reused := handler.playSessions.reuseCandidate(session, item.ID, item.ID, capabilities, allowTranscode, nil)
	if !reused {
		return nil
	}
	return detailMediaSourceDTOs(item, playID, descriptors, capabilities, allowTranscode)
}

func (handler *Handler) detailMediaSources(ctx context.Context, session AuthenticatedSession, item watchstate.CatalogTitle) []MediaSourceInfo {
	if handler == nil || handler.playback == nil || handler.playSessions == nil || item.ID == "" || item.MediaType != "movie" && item.MediaType != "episode" {
		return nil
	}
	capabilities, allowTranscode, err := handler.effectivePlaybackCapabilities(session, PlaybackInfoRequest{})
	if err != nil {
		return nil
	}
	options, releaseOptions, err := handler.playbackOptions(ctx, session, item, capabilities, allowTranscode)
	if err != nil {
		return nil
	}
	defer releaseOptions()
	playID, descriptors, reused := handler.playSessions.reuseCandidate(session, item.ID, item.ID, capabilities, allowTranscode, options)
	if !reused {
		playID, descriptors, err = handler.playSessions.register(ctx, session, item.ID, capabilities, allowTranscode, options)
		if err != nil {
			return nil
		}
	}
	return detailMediaSourceDTOs(item, playID, descriptors, capabilities, allowTranscode)
}

func detailMediaSourceDTOs(item watchstate.CatalogTitle, playID string, descriptors []playSourceDescriptor, capabilities playback.Capabilities, allowTranscode bool) []MediaSourceInfo {
	unspecifiedAudio := -1
	sources := make([]MediaSourceInfo, 0, len(descriptors))
	for _, descriptor := range descriptors {
		source := mediaSourceDTO(item, playID, descriptor, capabilities, allowTranscode, 0)
		source.DefaultAudioStreamIndex = &unspecifiedAudio
		sources = append(sources, source)
	}
	return sources
}

func (handler *Handler) playbackOptions(ctx context.Context, session AuthenticatedSession, item watchstate.CatalogTitle, capabilities playback.Capabilities, allowTranscode bool) ([]playback.SourceOption, func(), error) {
	resourceID := strings.TrimSpace(item.ResourceID)
	addonID := item.SourceAddonID
	if resourceID == "" && item.MediaType == "episode" {
		resourceID, addonID = handler.episodePlaybackIdentity(ctx, session, item)
	}
	if resourceID == "" {
		resourceID = preferredPlaybackResource(item.ProviderIDs)
	}
	if resourceID == "" {
		return nil, func() {}, playback.ErrNoPlayableSource
	}
	sourcesInput := playback.SourcesInput{
		MediaType: item.MediaType, AddonID: addonID, ResourceID: resourceID, Capabilities: capabilities,
		MaximumSources: maximumCompatibilityMediaSources,
	}
	var list playback.SourceList
	var err error
	reserved := false
	if reserver, ok := handler.playback.(sourceReferenceReserver); ok {
		list, err = reserver.SourcesAndPin(ctx, session.Principal, sourcesInput)
		reserved = err == nil
	} else {
		list, err = handler.playback.Sources(ctx, session.Principal, sourcesInput)
	}
	if err != nil {
		return nil, func() {}, err
	}
	var references []string
	if reserved {
		references = sourceOptionReferenceIDs(list.Sources)
	}
	if len(list.Sources) > maximumCompatibilityMediaSources {
		list.Sources = list.Sources[:maximumCompatibilityMediaSources]
	}
	release := func() {}
	if reserved {
		release = func() {
			handler.playback.(sourceReferenceReserver).UnpinSourceReferences(session.Principal, references)
		}
	}
	options := usableSourceOptions(list.Sources, capabilities, allowTranscode)
	if len(options) == 0 {
		release()
		return nil, func() {}, playback.ErrNoPlayableSource
	}
	return options, release, nil
}

func (handler *Handler) episodePlaybackIdentity(ctx context.Context, session AuthenticatedSession, item watchstate.CatalogTitle) (string, string) {
	if handler == nil || handler.catalog == nil || item.SeriesID == "" || item.ParentOrdinal == nil || item.Ordinal == nil || *item.ParentOrdinal < 0 || *item.Ordinal < 1 {
		return "", ""
	}
	series, err := handler.catalog.GetCatalogTitle(ctx, session.Principal, item.SeriesID)
	if err != nil || series.MediaType != "series" {
		return "", ""
	}
	resourceID := strings.TrimSpace(series.ResourceID)
	if resourceID == "" {
		resourceID = preferredPlaybackResource(series.ProviderIDs)
	}
	if resourceID == "" {
		return "", ""
	}
	return resourceID + ":" + strconv.Itoa(*item.ParentOrdinal) + ":" + strconv.Itoa(*item.Ordinal), series.SourceAddonID
}

func (handler *Handler) registerPlaybackInfo(ctx context.Context, session AuthenticatedSession, item watchstate.CatalogTitle, capabilities playback.Capabilities, allowTranscode bool, options []playback.SourceOption, mediaID string, startTimeTicks int64, audioIndex, subtitleIndex *int) (PlaybackInfoResponse, error) {
	playID, descriptors, err := handler.playSessions.register(ctx, session, item.ID, capabilities, allowTranscode, options)
	if err != nil {
		return PlaybackInfoResponse{}, err
	}
	if err := handler.playSessions.setPlaybackPreferences(session, item.ID, playID, mediaID, audioIndex, subtitleIndex); err != nil {
		if len(descriptors) > 0 {
			_ = handler.playSessions.close(context.WithoutCancel(ctx), session, item.ID, playID, descriptors[0].ID)
		}
		return PlaybackInfoResponse{}, err
	}
	result := PlaybackInfoResponse{PlaySessionId: playID, MediaSources: make([]MediaSourceInfo, 0, len(descriptors))}
	for _, descriptor := range descriptors {
		result.MediaSources = append(result.MediaSources, mediaSourceDTO(item, playID, descriptor, capabilities, allowTranscode, startTimeTicks))
	}
	return result, nil
}

func (handler *Handler) handleStream(response http.ResponseWriter, request *http.Request) {
	if handler.playback == nil || handler.catalog == nil || handler.playSessions == nil {
		http.NotFound(response, request)
		return
	}
	request = streamRequestWithPathSelectors(request)
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		handler.writeStreamError(response, request, http.StatusBadRequest, "InvalidRequest", "The stream request is invalid")
		return
	}
	itemID := streamRequestItemID(request)
	playID, mediaID, startTimeTicks, selectorErr := parseStreamSelectors(request.URL.Query())
	var session AuthenticatedSession
	var item watchstate.CatalogTitle
	var ok bool
	if selectorErr != nil {
		fallbackPlayID, _, fallbackPlayIDErr := queryScalar(request.URL.Query(), "PlaySessionId")
		if fallbackPlayIDErr != nil || !legacyStreamFallbackAllowed(request, fallbackPlayID) {
			handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The playback session is invalid or expired")
			return
		}
		session, item, playID, mediaID, startTimeTicks, ok = handler.prepareStaticStream(response, request, itemID)
		if !ok {
			return
		}
	} else {
		session, ok = handler.authenticateStreamRequest(response, request, itemID, playID, mediaID)
		if !ok {
			return
		}
		var err error
		item, err = handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID)
		if err != nil {
			handler.writeStreamPlaybackError(response, request, err)
			return
		}
	}
	binding, handle, resolved, err := handler.playSessions.openAndTouch(request.Context(), session, item.ID, playID, mediaID, startTimeTicks)
	if err != nil && legacyStreamFallbackAllowed(request, playID) {
		session, item, playID, mediaID, startTimeTicks, ok = handler.prepareStaticStream(response, request, itemID)
		if !ok {
			return
		}
		binding, handle, resolved, err = handler.playSessions.openAndTouch(request.Context(), session, item.ID, playID, mediaID, startTimeTicks)
	}
	if err != nil {
		handler.writeStreamPlaybackError(response, request, err)
		return
	}
	if binding.ItemID != item.ID || binding.MediaSourceID != mediaID {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The playback session is invalid or expired")
		return
	}
	if legacyProcessingStreamRequest(request, resolved) {
		response.Header().Set("Cache-Control", "no-store")
		http.Redirect(response, request, compatibilityMasterPath(item.ID, compatibilityPlaybackValues(playID, mediaID, startTimeTicks)), http.StatusFound)
		return
	}
	deliveryRequest := scopedStreamDeliveryRequest(request, playID, mediaID)
	if err := handler.playback.Serve(response, deliveryRequest, handle); err != nil {
		if playback.IsTerminalDeliveryError(err) {
			_ = handler.playSessions.close(context.WithoutCancel(request.Context()), session, item.ID, playID, mediaID)
		}
		handler.writeStreamPlaybackError(response, request, err)
	}
}

func (handler *Handler) handleSubtitleStream(response http.ResponseWriter, request *http.Request) {
	if handler.playback == nil || handler.catalog == nil || handler.playSessions == nil {
		http.NotFound(response, request)
		return
	}
	if request == nil || request.URL == nil || len(request.URL.RawQuery) > maximumCompatRawQueryBytes || validateQueryBudget(request.URL.Query()) != nil {
		http.NotFound(response, request)
		return
	}
	request, subtitleIndex, ok := subtitleStreamRequest(request)
	if !ok {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The subtitle stream is invalid or expired")
		return
	}
	itemID, validItemID := canonicalCompatUUID(request.PathValue("id"))
	pathMediaID, validPathMediaID := canonicalCompatUUID(request.PathValue("mediaSourceId"))
	if !validItemID || !validPathMediaID {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The subtitle stream is invalid or expired")
		return
	}
	request, ok = subtitleStreamRequestWithPathMediaSource(request, pathMediaID)
	if !ok {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The subtitle stream is invalid or expired")
		return
	}
	values := request.URL.Query()
	queryPlayID, hasPlayID, selectorErr := queryScalar(values, "PlaySessionId")
	if selectorErr != nil {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The subtitle stream is invalid or expired")
		return
	}
	parseValues := values
	if !hasPlayID {
		parseValues.Set("PlaySessionId", playSessionIDPrefix+strings.Repeat("0", 16))
	}
	playID, mediaID, startTimeTicks, err := parseStreamSelectors(parseValues)
	if err != nil || mediaID != pathMediaID {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The subtitle stream is invalid or expired")
		return
	}
	if hasPlayID {
		playID = queryPlayID
	}
	var session AuthenticatedSession
	if hasPlayID {
		session, ok = handler.authenticateStreamRequest(response, request, itemID, playID, mediaID)
	} else {
		session, ok = handler.authenticateGeneralStreamRequest(response, request)
	}
	if !ok {
		return
	}
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID)
	if err != nil || item.ID != itemID || item.MediaType != "movie" && item.MediaType != "episode" {
		if err == nil {
			err = watchstate.ErrNotFound
		}
		handler.writeStreamPlaybackError(response, request, err)
		return
	}
	if !hasPlayID {
		if playID, ok = handler.playSessions.reuseSubtitleCandidate(session, itemID, mediaID); !ok {
			handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The subtitle stream is invalid or expired")
			return
		}
	}
	binding, handle, native, err := handler.playSessions.openAndTouch(request.Context(), session, itemID, playID, mediaID, startTimeTicks)
	if err != nil {
		handler.writeStreamPlaybackError(response, request, err)
		return
	}
	if binding.ItemID != itemID || binding.PlaySessionID != playID || binding.MediaSourceID != mediaID {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The subtitle stream is invalid or expired")
		return
	}
	assetID, found := compatibilitySubtitleAssetID(native, subtitleIndex)
	if !found {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The subtitle stream is invalid or expired")
		return
	}
	if err := handler.playback.ServeAsset(response, scopedSubtitleDeliveryRequest(request), handle, assetID); err != nil {
		if playback.IsTerminalDeliveryError(err) {
			_ = handler.playSessions.close(context.WithoutCancel(request.Context()), session, itemID, playID, mediaID)
		}
		handler.writeStreamPlaybackError(response, request, err)
	}
}

func subtitleStreamRequest(request *http.Request) (*http.Request, int, bool) {
	if request == nil || request.URL == nil || request.PathValue("format") != "vtt" {
		return request, 0, false
	}
	rawIndex := request.PathValue("subtitleIndex")
	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 || index > maximumCompatibilitySubtitleIndex || strconv.Itoa(index) != rawIndex {
		return request, 0, false
	}
	rawStart := request.PathValue("startPositionTicks")
	if rawStart == "" {
		return request, index, true
	}
	start, err := strconv.ParseInt(rawStart, 10, 64)
	if err != nil || start < 0 || start > 7*24*60*60*TicksPerSecond || strconv.FormatInt(start, 10) != rawStart {
		return request, 0, false
	}
	cloned := request.Clone(request.Context())
	clonedURL := *request.URL
	values := clonedURL.Query()
	if queryStart, found, scalarErr := queryScalar(values, "StartTimeTicks"); scalarErr != nil || found && queryStart != rawStart {
		return request, 0, false
	} else if !found {
		values.Set("StartTimeTicks", rawStart)
	}
	clonedURL.RawQuery = values.Encode()
	cloned.URL = &clonedURL
	return cloned, index, true
}

func subtitleStreamRequestWithPathMediaSource(request *http.Request, pathMediaID string) (*http.Request, bool) {
	if request == nil || request.URL == nil || pathMediaID == "" {
		return request, false
	}
	values := request.URL.Query()
	mediaID, mediaFound, err := queryScalar(values, "MediaSourceId")
	if err != nil {
		return request, false
	}
	if mediaFound {
		canonicalMediaID, valid := canonicalCompatUUID(mediaID)
		if !valid || canonicalMediaID != pathMediaID {
			return request, false
		}
	} else {
		values.Set("MediaSourceId", pathMediaID)
	}
	playID, playFound, err := queryScalar(values, "PlaySessionId")
	if err != nil {
		return request, false
	}
	credential, credentialFound, err := queryScalar(values, "api_key")
	if err != nil {
		return request, false
	}
	if credentialFound && isRivunePlaySessionID(credential) {
		if playFound && playID != credential {
			return request, false
		}
		if !playFound {
			values.Set("PlaySessionId", credential)
		}
	}
	cloned := request.Clone(request.Context())
	clonedURL := *request.URL
	clonedURL.RawQuery = values.Encode()
	cloned.URL = &clonedURL
	return cloned, true
}

func compatibilitySubtitleAssetID(session playback.Session, subtitleIndex int) (string, bool) {
	for _, source := range session.Sources {
		if source.ID != session.SelectedSourceID || source.Media == nil {
			continue
		}
		streams, _ := compatibilityMediaStreams(source.Media, session.SelectedAudioTrack)
		for _, binding := range compatibilitySubtitleBindings(streams, session.Subtitles) {
			if binding.index == subtitleIndex {
				return binding.assetID, true
			}
		}
		break
	}
	return "", false
}

func scopedSubtitleDeliveryRequest(request *http.Request) *http.Request {
	cloned := request.Clone(request.Context())
	clonedURL := *request.URL
	clonedURL.RawQuery = ""
	cloned.URL = &clonedURL
	cloned.Header = request.Header.Clone()
	deleteCompatCredentialHeaders(cloned.Header)
	return cloned
}

func streamRequestWithPathSelectors(request *http.Request) *http.Request {
	if request == nil || request.PathValue("playlistId") == "" {
		return request
	}
	cloned := request.Clone(request.Context())
	clonedURL := *request.URL
	values := clonedURL.Query()
	if value, found, err := queryScalar(values, "PlaySessionId"); err == nil {
		if !found {
			values.Set("PlaySessionId", request.PathValue("playlistId"))
		} else if value != request.PathValue("playlistId") {
			values.Add("PlaySessionId", request.PathValue("playlistId"))
		}
	}
	if value, found, err := queryScalar(values, "RivuneChildId"); err == nil && request.PathValue("segmentId") != "" {
		if !found {
			values.Set("RivuneChildId", request.PathValue("segmentId"))
		} else if value != request.PathValue("segmentId") {
			values.Add("RivuneChildId", request.PathValue("segmentId"))
		}
	}
	clonedURL.RawQuery = values.Encode()
	cloned.URL = &clonedURL
	return cloned
}

func streamRequestItemID(request *http.Request) string {
	value := request.PathValue("id")
	if value == "" {
		value = request.PathValue("itemId")
	}
	canonical, _ := canonicalCompatUUID(value)
	return canonical
}

func (handler *Handler) prepareStaticStream(response http.ResponseWriter, request *http.Request, itemID string) (AuthenticatedSession, watchstate.CatalogTitle, string, string, int64, bool) {
	session, ok := handler.authenticateGeneralStreamRequest(response, request)
	if !ok {
		return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
	}
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID)
	if err != nil || item.ID == "" || item.MediaType != "movie" && item.MediaType != "episode" {
		if err == nil {
			err = watchstate.ErrNotFound
		}
		handler.writeStreamPlaybackError(response, request, err)
		return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
	}
	input, err := parsePlaybackInfoRequest(response, request)
	if err != nil {
		handler.writeStreamError(response, request, http.StatusBadRequest, "InvalidRequest", "The stream request is invalid")
		return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
	}
	capabilities, allowTranscode, err := handler.effectivePlaybackCapabilities(session, input)
	if err != nil {
		handler.writeStreamPlaybackError(response, request, err)
		return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
	}
	if raw, found, scalarErr := queryScalar(request.URL.Query(), "SegmentContainer"); scalarErr != nil {
		handler.writeStreamError(response, request, http.StatusBadRequest, "InvalidRequest", "The stream request is invalid")
		return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
	} else if found {
		raw = normalizeCapability(raw)
		if raw != "ts" && raw != "mp4" {
			handler.writeStreamError(response, request, http.StatusBadRequest, "InvalidRequest", "The stream request is invalid")
			return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
		}
		capabilities.HLSSegmentContainer = raw
	}
	requestedMediaID := input.MediaSourceId
	if requestedMediaID == "" {
		requestedMediaID = item.ID
	}
	options, releaseOptions, err := handler.playbackOptions(request.Context(), session, item, capabilities, allowTranscode)
	if err != nil {
		handler.writeStreamPlaybackError(response, request, err)
		return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
	}
	defer releaseOptions()
	playID, _, reused := handler.playSessions.reuseCandidate(session, item.ID, requestedMediaID, capabilities, allowTranscode, options)
	if !reused {
		if requestedMediaID != item.ID {
			handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The playback source is invalid or expired")
			return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
		}
		var descriptors []playSourceDescriptor
		playID, descriptors, err = handler.playSessions.register(request.Context(), session, item.ID, capabilities, allowTranscode, options)
		if err != nil || len(descriptors) == 0 || descriptors[0].ID != item.ID {
			if err == nil {
				err = errPlaySessionNotFound
			}
			handler.writeStreamPlaybackError(response, request, err)
			return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
		}
	}
	if err := handler.playSessions.setPlaybackPreferences(session, item.ID, playID, requestedMediaID, input.AudioStreamIndex, input.SubtitleStreamIndex); err != nil {
		handler.writeStreamPlaybackError(response, request, err)
		return AuthenticatedSession{}, watchstate.CatalogTitle{}, "", "", 0, false
	}
	return session, item, playID, requestedMediaID, input.StartTimeTicks, true
}

func (handler *Handler) authenticateGeneralStreamRequest(response http.ResponseWriter, request *http.Request) (AuthenticatedSession, bool) {
	token, found, err := extractCompatToken(request)
	if err != nil || !found {
		handler.writeStreamError(response, request, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
		return AuthenticatedSession{}, false
	}
	session, err := handler.authentication.Authenticate(request.Context(), token)
	if err != nil || !validAuthenticatedSession(session) {
		if handler.writeCompatStreamRequestPolicyError(response, request, err) {
			return AuthenticatedSession{}, false
		}
		if errors.Is(err, ErrCompatAuthenticationSaturated) {
			handler.writeStreamError(response, request, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility authentication service is busy")
		} else if err != nil && !errors.Is(err, ErrInvalidCompatCredential) && !errors.Is(err, ErrInvalidCompatAuthorization) {
			handler.writeStreamError(response, request, http.StatusInternalServerError, "InternalError", "The stream authorization could not be verified")
		} else {
			handler.writeStreamError(response, request, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
		}
		return AuthenticatedSession{}, false
	}
	if !requestUserMatchesSession(request, session.ProfileID) {
		handler.writeStreamError(response, request, http.StatusNotFound, "ResourceNotFound", "The requested resource was not found")
		return AuthenticatedSession{}, false
	}
	return session, true
}

func (handler *Handler) handleDownload(response http.ResponseWriter, request *http.Request) {
	if handler.playback == nil || handler.catalog == nil || handler.playSessions == nil || request == nil || request.URL == nil {
		http.NotFound(response, request)
		return
	}
	if len(request.URL.RawQuery) > maximumCompatRawQueryBytes {
		handler.writeStreamError(response, request, http.StatusBadRequest, "InvalidRequest", "The download request is invalid")
		return
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || validateDownloadQuery(values) != nil {
		handler.writeStreamError(response, request, http.StatusBadRequest, "InvalidRequest", "The download request is invalid")
		return
	}
	session, ok := handler.authenticateGeneralStreamRequest(response, request)
	if !ok {
		return
	}
	itemID := streamRequestItemID(request)
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID)
	if err != nil || item.ID != itemID || item.MediaType != "movie" && item.MediaType != "episode" {
		if err == nil || errors.Is(err, watchstate.ErrForbidden) {
			err = watchstate.ErrNotFound
		}
		handler.writeStreamPlaybackError(response, request, err)
		return
	}
	mediaID, found, err := queryScalar(values, "MediaSourceId")
	if err != nil {
		handler.writeStreamError(response, request, http.StatusBadRequest, "InvalidRequest", "The download request is invalid")
		return
	}
	if found {
		mediaID, ok = canonicalCompatUUID(mediaID)
		if !ok || mediaID != item.ID {
			handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The download source is invalid or expired")
			return
		}
	}

	capabilities := downloadCapabilities()
	options, releaseOptions, err := handler.playbackOptions(request.Context(), session, item, capabilities, false)
	if err != nil {
		handler.logPlaybackInfoSourceOpenFailure(PlaybackInfoRequest{}, capabilities, false, "download_options", err)
		handler.writeStreamPlaybackError(response, request, err)
		return
	}
	defer releaseOptions()
	options = downloadableSourceOptions(options)
	if len(options) == 0 {
		handler.writeStreamPlaybackError(response, request, playback.ErrUnsupportedSource)
		return
	}
	playID, descriptors, err := handler.playSessions.register(request.Context(), session, item.ID, capabilities, false, options)
	if err != nil || len(descriptors) == 0 || descriptors[0].ID != item.ID {
		if err == nil {
			err = errPlaySessionNotFound
		}
		handler.logPlaybackInfoSourceOpenFailure(PlaybackInfoRequest{}, capabilities, false, "download_register", err)
		handler.writeStreamPlaybackError(response, request, err)
		return
	}
	mediaID = descriptors[0].ID
	defer func() {
		_ = handler.playSessions.close(context.WithoutCancel(request.Context()), session, item.ID, playID, mediaID)
	}()
	var handle playback.DeliveryHandle
	var selected playback.Source
	var lastOpenErr error
	resolved := false
	for _, descriptor := range descriptors {
		if found && descriptor.ID != mediaID {
			continue
		}
		binding, candidateHandle, native, openErr := handler.playSessions.openAndTouch(request.Context(), session, item.ID, playID, descriptor.ID, 0)
		if openErr != nil {
			lastOpenErr = openErr
			continue
		}
		candidate, candidateResolved := selectedPlaybackSource(native)
		if binding.ItemID != item.ID || binding.MediaSourceID != descriptor.ID || !candidateResolved ||
			normalizeCapability(candidate.Mode) != "direct" || normalizeCapability(candidate.Protocol) != "http" || !downloadableContainer(candidate.Container) {
			lastOpenErr = playback.ErrUnsupportedSource
			continue
		}
		mediaID, handle, selected, resolved = descriptor.ID, candidateHandle, candidate, true
		break
	}
	if !resolved {
		if lastOpenErr == nil {
			lastOpenErr = playback.ErrUnsupportedSource
		}
		handler.logPlaybackInfoSourceOpenFailure(PlaybackInfoRequest{}, capabilities, false, "download_open", lastOpenErr)
		handler.writeStreamPlaybackError(response, request, lastOpenErr)
		return
	}
	downloadResponse := &downloadResponseWriter{
		ResponseWriter: response,
		disposition:    mime.FormatMediaType("attachment", map[string]string{"filename": downloadFilename(item, selected.Container)}),
		fallbackType:   downloadContentType(selected.Container),
	}
	if err := handler.playback.ServeAsset(downloadResponse, scopedDownloadDeliveryRequest(request, playID, mediaID), handle, selected.ID); err != nil {
		if downloadResponse.upstreamFailure {
			downloadResponse.reset()
		}
		handler.writeStreamPlaybackError(response, request, err)
		return
	}
	if downloadResponse.upstreamFailure {
		downloadResponse.reset()
		handler.writeStreamError(response, request, http.StatusBadGateway, "PlaybackSourceFailed", "The media source is unavailable")
	}
}

func validateDownloadQuery(values url.Values) error {
	if validateQueryBudget(values) != nil {
		return ErrInvalidQuery
	}
	for name := range values {
		switch strings.ToLower(name) {
		case "apikey", "api_key", "x-emby-token", "x-mediabrowser-token", "userid", "mediasourceid":
		default:
			return ErrInvalidQuery
		}
	}
	for _, name := range []string{"UserId", "MediaSourceId"} {
		if _, _, err := queryScalar(values, name); err != nil {
			return err
		}
	}
	return nil
}

func downloadCapabilities() playback.Capabilities {
	preferDirect := true
	return playback.Capabilities{
		StreamingProtocols: []string{"http"},
		Containers: []string{
			"3gp", "avi", "flv", "m2ts", "m4v", "mkv", "mov", "mp4", "mpeg", "mpg", "mts", "ogv", "ts", "webm", "wmv",
		},
		HDRFormats:             []string{"dolby_vision", "hdr10", "hlg"},
		PreferDirectPlay:       &preferDirect,
		AllowDirectPassthrough: true,
	}
}

func downloadableSourceOptions(options []playback.SourceOption) []playback.SourceOption {
	result := make([]playback.SourceOption, 0, len(options))
	for _, option := range options {
		if normalizeCapability(option.Protocol) == "http" && option.SourceRef != "" {
			result = append(result, option)
		}
	}
	return result
}

func downloadableContainer(value string) bool {
	return canonicalDownloadContainer(value) != ""
}

func canonicalDownloadContainer(value string) string {
	best, bestRank := "", 16
	for candidate := range strings.SplitSeq(value, ",") {
		candidate = normalizedContainer(candidate)
		switch candidate {
		case "matroska":
			candidate = "mkv"
		case "asf":
			candidate = "wmv"
		case "mpegvideo":
			candidate = "mpeg"
		}
		rank := 16
		for index, supported := range []string{"mp4", "mkv", "webm", "mov", "m4v", "avi", "mpeg", "mpg", "m2ts", "mts", "ts", "ogv", "3gp", "flv", "wmv"} {
			if candidate == supported {
				rank = index
				break
			}
		}
		if rank < bestRank {
			best, bestRank = candidate, rank
		}
	}
	return best
}

func scopedDownloadDeliveryRequest(request *http.Request, playID, mediaID string) *http.Request {
	cloned := request.Clone(request.Context())
	clonedURL := *request.URL
	values := make(url.Values, 3)
	values.Set("api_key", playID)
	values.Set("PlaySessionId", playID)
	values.Set("MediaSourceId", mediaID)
	clonedURL.RawQuery = values.Encode()
	cloned.URL = &clonedURL
	cloned.Header = request.Header.Clone()
	deleteCompatCredentialHeaders(cloned.Header)
	return cloned
}

func downloadFilename(item watchstate.CatalogTitle, container string) string {
	name := strings.TrimSpace(item.Title)
	name = strings.ReplaceAll(name, "\\", "/")
	if separator := strings.LastIndexByte(name, '/'); separator >= 0 {
		name = name[separator+1:]
	}
	runes := make([]rune, 0, 120)
	for _, value := range name {
		switch value {
		case '/', '\\', '<', '>', ':', '"', '|', '?', '*':
			continue
		}
		if unicode.IsControl(value) {
			continue
		}
		runes = append(runes, value)
	}
	name = strings.Trim(strings.TrimSpace(string(runes)), ".")
	if name == "" {
		name = "download-" + item.ID
	}
	extension := "." + canonicalDownloadContainer(container)
	if extension != "." && !strings.HasSuffix(strings.ToLower(name), extension) {
		name += extension
	}
	return name
}

func downloadContentType(container string) string {
	switch canonicalDownloadContainer(container) {
	case "mp4", "m4v":
		return "video/mp4"
	case "mov":
		return "video/quicktime"
	case "mkv":
		return "video/x-matroska"
	case "webm":
		return "video/webm"
	case "avi":
		return "video/x-msvideo"
	case "mpeg", "mpg":
		return "video/mpeg"
	case "m2ts", "mts", "ts":
		return "video/mp2t"
	case "ogv":
		return "video/ogg"
	case "3gp":
		return "video/3gpp"
	case "flv":
		return "video/x-flv"
	default:
		return "application/octet-stream"
	}
}

type downloadResponseWriter struct {
	http.ResponseWriter
	disposition     string
	fallbackType    string
	wroteHeader     bool
	discardBody     bool
	upstreamFailure bool
}

func (writer *downloadResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		if status == http.StatusRequestedRangeNotSatisfiable {
			writer.sanitizeHeaders(false)
			writer.discardBody = true
			writer.ResponseWriter.WriteHeader(status)
			return
		}
		writer.upstreamFailure = true
		writer.discardBody = true
		return
	}
	writer.sanitizeHeaders(true)
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *downloadResponseWriter) Write(body []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	if writer.discardBody {
		return len(body), nil
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *downloadResponseWriter) sanitizeHeaders(success bool) {
	header := writer.ResponseWriter.Header()
	for _, name := range []string{"ETag", "Expires", "Last-Modified", "Location", "Set-Cookie", "WWW-Authenticate"} {
		header.Del(name)
	}
	header.Set("Cache-Control", "no-store")
	if ranges := header.Get("Accept-Ranges"); ranges != "" {
		if ranges == "bytes" {
			header.Set("Accept-Ranges", ranges)
		} else {
			header.Del("Accept-Ranges")
		}
	}
	if contentRange := header.Get("Content-Range"); contentRange != "" {
		if safeDownloadContentRange(contentRange) {
			header.Set("Content-Range", contentRange)
		} else {
			header.Del("Content-Range")
		}
	}
	if !success {
		header.Del("Content-Disposition")
		header.Del("Content-Length")
		header.Del("Content-Type")
		return
	}
	header.Set("Content-Disposition", writer.disposition)
	contentType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || !safeDownloadContentType(contentType) {
		contentType = writer.fallbackType
	}
	header.Set("Content-Type", contentType)
	if length := header.Get("Content-Length"); length != "" {
		parsed, parseErr := strconv.ParseInt(length, 10, 64)
		if parseErr != nil || parsed < 0 || strconv.FormatInt(parsed, 10) != length {
			header.Del("Content-Length")
		} else {
			header.Set("Content-Length", length)
		}
	}
}

func safeDownloadContentRange(value string) bool {
	if !strings.HasPrefix(value, "bytes ") || len(value) <= len("bytes ") || len(value) > 96 {
		return false
	}
	for _, character := range value[len("bytes "):] {
		if character < '0' || character > '9' {
			switch character {
			case '-', '/', '*':
			default:
				return false
			}
		}
	}
	return strings.ContainsRune(value, '/')
}

func safeDownloadContentType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "video/") || strings.HasPrefix(value, "audio/") ||
		value == "application/octet-stream" || value == "application/ogg" || value == "application/x-matroska"
}

func (writer *downloadResponseWriter) reset() {
	header := writer.ResponseWriter.Header()
	for name := range header {
		header.Del(name)
	}
}

func (handler *Handler) authenticateStreamRequest(response http.ResponseWriter, request *http.Request, itemID, playID, mediaID string) (AuthenticatedSession, bool) {
	token, found, parseErr := extractCompatToken(request)
	if parseErr == nil && found {
		session, authErr := handler.authentication.Authenticate(request.Context(), token)
		if authErr == nil {
			if !validAuthenticatedSession(session) {
				handler.writeStreamError(response, request, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token is required")
				return AuthenticatedSession{}, false
			}
			if !requestUserMatchesSession(request, session.ProfileID) {
				handler.writeStreamError(response, request, http.StatusNotFound, "ResourceNotFound", "The requested resource was not found")
				return AuthenticatedSession{}, false
			}
			return session, true
		}
		if handler.writeCompatStreamRequestPolicyError(response, request, authErr) {
			return AuthenticatedSession{}, false
		}
		if errors.Is(authErr, ErrCompatAuthenticationSaturated) {
			handler.writeStreamError(response, request, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility authentication service is busy")
			return AuthenticatedSession{}, false
		}
		if !errors.Is(authErr, ErrInvalidCompatCredential) && !errors.Is(authErr, ErrInvalidCompatAuthorization) {
			handler.writeStreamError(response, request, http.StatusInternalServerError, "InternalError", "The stream authorization could not be verified")
			return AuthenticatedSession{}, false
		}
	}

	// Media players frequently replace or drop Jellyfin credentials after
	// PlaybackInfo. The negotiated PlaySessionId is an opaque, short-lived
	// capability already bound to owner, item, source and TTL in the registry.
	cached, valid := handler.playSessions.streamSession(playID, itemID, mediaID)
	if !valid {
		handler.writeStreamError(response, request, http.StatusUnauthorized, "Unauthorized", "A valid compatibility token or playback session is required")
		return AuthenticatedSession{}, false
	}
	fresh, revalidateErr := handler.authentication.Revalidate(request.Context(), cached)
	ownerMismatch := revalidateErr == nil && !sameAuthenticatedSessionOwner(cached, fresh)
	if revalidateErr != nil || ownerMismatch {
		if handler.writeCompatStreamRequestPolicyError(response, request, revalidateErr) {
			return AuthenticatedSession{}, false
		}
		if errors.Is(revalidateErr, ErrCompatAuthenticationSaturated) {
			handler.writeStreamError(response, request, http.StatusServiceUnavailable, "ServiceUnavailable", "The compatibility authentication service is busy")
		} else if errors.Is(revalidateErr, ErrInvalidCompatCredential) || ownerMismatch {
			_ = handler.playSessions.close(context.WithoutCancel(request.Context()), cached, itemID, playID, mediaID)
			handler.writeStreamError(response, request, http.StatusUnauthorized, "Unauthorized", "The playback session is no longer valid")
		} else {
			handler.writeStreamError(response, request, http.StatusInternalServerError, "InternalError", "The stream authorization could not be verified")
		}
		return AuthenticatedSession{}, false
	}
	return fresh, true
}

func (registry *playSessionRegistry) reuseSubtitleCandidate(session AuthenticatedSession, itemID, mediaID string) (string, bool) {
	if registry == nil || !validPlaySessionOwner(session) || itemID == "" || mediaID == "" {
		return "", false
	}
	now := registry.now().UTC()
	registry.mu.Lock()
	var selected *playSessionEntry
	for _, entry := range registry.entries {
		if !ownerMatches(entry, session) || entry.itemID != itemID || entry.sources[mediaID] == nil ||
			!entry.expiresAt.After(now) || !entry.lastSeenAt.Add(registry.idleTTL).After(now) {
			continue
		}
		if selected == nil || entry.sequence > selected.sequence {
			selected = entry
		}
	}
	if selected == nil {
		registry.mu.Unlock()
		return "", false
	}
	capabilities := clonePlaybackCapabilities(selected.capabilities)
	allowTranscode := selected.allowTranscode
	registry.mu.Unlock()
	playID, _, reused := registry.reuseCandidate(session, itemID, mediaID, capabilities, allowTranscode, nil)
	return playID, reused
}

func scopedStreamDeliveryRequest(request *http.Request, playID, mediaID string) *http.Request {
	cloned := request.Clone(request.Context())
	clonedURL := *request.URL
	values := clonedURL.Query()
	deleteQueryFold(values, "ApiKey")
	deleteQueryFold(values, "api_key")
	deleteQueryFold(values, "X-Emby-Token")
	deleteQueryFold(values, "X-MediaBrowser-Token")

	values.Set("api_key", playID)
	values.Set("PlaySessionId", playID)
	values.Set("MediaSourceId", mediaID)
	clonedURL.RawQuery = values.Encode()
	cloned.URL = &clonedURL
	deleteCompatCredentialHeaders(cloned.Header)

	return cloned
}

func deleteCompatCredentialHeaders(headers http.Header) {
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token", "X-Emby-Authorization", "X-MediaBrowser-Authorization", "Authorization"} {
		headers.Del(name)
	}
}

func deleteQueryFold(values url.Values, name string) {
	for actualName := range values {
		if strings.EqualFold(actualName, name) {
			delete(values, actualName)
		}
	}
}

func parsePlaybackInfoRequest(response http.ResponseWriter, request *http.Request) (PlaybackInfoRequest, error) {
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		return PlaybackInfoRequest{}, err
	}
	var input PlaybackInfoRequest
	if request.Body != nil && request.Body != http.NoBody {
		limited := http.MaxBytesReader(response, request.Body, maximumPlaybackInfoBodyBytes)
		decoder := json.NewDecoder(limited)
		if err := decoder.Decode(&input); err != nil && !errors.Is(err, io.EOF) {
			return PlaybackInfoRequest{}, err
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return PlaybackInfoRequest{}, errors.New("multiple playback request documents")
		}
	}
	values := request.URL.Query()
	if value, found, err := queryScalar(values, "UserId"); err != nil {
		return PlaybackInfoRequest{}, err
	} else if found {
		input.UserId = value
	}
	if value, found, err := queryScalar(values, "MediaSourceId"); err != nil {
		return PlaybackInfoRequest{}, err
	} else if found {
		input.MediaSourceId = value
	}
	if value, found, err := queryScalar(values, "StartTimeTicks"); err != nil {
		return PlaybackInfoRequest{}, err
	} else if found {
		parsed, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || parsed < 0 {
			return PlaybackInfoRequest{}, ErrInvalidQuery
		}
		input.StartTimeTicks = parsed
	}
	if value, found, err := queryScalar(values, "MaxStreamingBitrate"); err != nil {
		return PlaybackInfoRequest{}, err
	} else if found {
		parsed, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || parsed <= 0 {
			return PlaybackInfoRequest{}, ErrInvalidQuery
		}
		input.MaxStreamingBitrate = parsed
	}
	if err := parsePlaybackQueryOptions(values, &input); err != nil {
		return PlaybackInfoRequest{}, err
	}
	if input.UserId != "" {
		canonicalUserID, valid := canonicalCompatUUID(input.UserId)
		if !valid {
			return PlaybackInfoRequest{}, ErrInvalidQuery
		}
		input.UserId = canonicalUserID
	}
	if canonicalMediaID, valid := canonicalCompatUUID(input.MediaSourceId); valid {
		input.MediaSourceId = canonicalMediaID
	}
	if len(input.UserId) > 128 || len(input.MediaSourceId) > 128 || input.StartTimeTicks < 0 || input.StartTimeTicks > 7*24*60*60*TicksPerSecond ||
		input.MaxStreamingBitrate < 0 || input.MaxStreamingBitrate > maximumCompatClientBitrate || input.MaxAudioChannels < 0 || input.MaxAudioChannels > 32 ||
		!validPlaybackTrackIndex(input.AudioStreamIndex) || !validPlaybackTrackIndex(input.SubtitleStreamIndex) {
		return PlaybackInfoRequest{}, ErrInvalidQuery
	}
	return input, nil
}

func validPlaybackTrackIndex(value *int) bool {
	return value == nil || *value >= -1 && *value <= 1024
}

func parsePlaybackQueryOptions(values url.Values, input *PlaybackInfoRequest) error {
	if input == nil {
		return ErrInvalidQuery
	}
	optionalBoolean := func(name string, target **bool) error {
		raw, found, err := queryScalar(values, name)
		if err != nil || !found {
			return err
		}
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return ErrInvalidQuery
		}
		*target = &value
		return nil
	}
	for _, option := range []struct {
		name   string
		target **bool
	}{
		{"EnableDirectPlay", &input.EnableDirectPlay},
		{"EnableDirectStream", &input.EnableDirectStream},
		{"EnableTranscoding", &input.EnableTranscoding},
		{"AllowVideoStreamCopy", &input.AllowVideoStreamCopy},
		{"AllowAudioStreamCopy", &input.AllowAudioStreamCopy},
	} {
		if err := optionalBoolean(option.name, option.target); err != nil {
			return err
		}
	}
	optionalIndex := func(name string, target **int) error {
		raw, found, err := queryScalar(values, name)
		if err != nil || !found {
			return err
		}
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < -1 || value > 1024 {
			return ErrInvalidQuery
		}
		parsed := int(value)
		*target = &parsed
		return nil
	}
	if err := optionalIndex("AudioStreamIndex", &input.AudioStreamIndex); err != nil {
		return err
	}
	if err := optionalIndex("SubtitleStreamIndex", &input.SubtitleStreamIndex); err != nil {
		return err
	}
	if raw, found, err := queryScalar(values, "MaxAudioChannels"); err != nil {
		return err
	} else if found {
		value, parseErr := strconv.ParseInt(raw, 10, 32)
		if parseErr != nil || value < 0 || value > 32 {
			return ErrInvalidQuery
		}
		input.MaxAudioChannels = int(value)
	}
	return nil
}

func playbackFlag(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
func emptyDeviceProfile(profile DeviceProfile) bool {
	return len(profile.CodecProfiles) == 0 && len(profile.ContainerProfiles) == 0 && len(profile.DirectPlayProfiles) == 0 &&
		len(profile.TranscodingProfiles) == 0 && len(profile.SubtitleProfiles) == 0
}

// GET PlaybackInfo has no DeviceProfile body in the Jellyfin protocol. This
// conservative baseline is limited to formats supported by both target clients;
// POST callers still have to announce their actual capabilities.
func conservativeCompatibilityProfile() DeviceProfile {
	return DeviceProfile{
		DirectPlayProfiles:  []DirectPlayProfile{{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Type: "Video"}},
		TranscodingProfiles: []TranscodingProfile{{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Protocol: "hls", Context: "Streaming", Type: "Video"}},
		SubtitleProfiles:    []SubtitleProfile{{Format: "srt,vtt", Method: "External"}},
	}
}
func (handler *Handler) effectivePlaybackCapabilities(session AuthenticatedSession, input PlaybackInfoRequest) (playback.Capabilities, bool, error) {
	if emptyDeviceProfile(input.DeviceProfile) {
		if stored, ok := handler.playSessions.deviceProfile(session); ok {
			input.DeviceProfile = stored
		} else {
			input.DeviceProfile = conservativeCompatibilityProfile()
		}
	}
	capabilities, allowTranscode, err := playbackCapabilities(input)
	if err == nil || !validDeviceProfileBounds(input.DeviceProfile) {
		return capabilities, allowTranscode, err
	}
	fallback := conservativeCompatibilityProfile()
	fallback.MaxStreamingBitrate = input.DeviceProfile.MaxStreamingBitrate
	input.DeviceProfile = fallback
	return playbackCapabilities(input)
}

func validDeviceProfileBounds(profile DeviceProfile) bool {
	if len(profile.Name) > 128 || len(profile.CodecProfiles) > maximumRawDeviceProfileEntries || len(profile.ContainerProfiles) > maximumRawDeviceProfileEntries ||
		len(profile.DirectPlayProfiles) > maximumRawDeviceProfileEntries || len(profile.TranscodingProfiles) > maximumRawDeviceProfileEntries || len(profile.SubtitleProfiles) > maximumRawDeviceProfileEntries ||
		profile.MaxStreamingBitrate != 0 && (profile.MaxStreamingBitrate < 64_000 || profile.MaxStreamingBitrate > maximumCompatClientBitrate) {
		return false
	}
	boundedList := func(value string) bool { return len(value) <= 2048 }
	boundedScalar := func(value string) bool { return len(value) <= 64 }
	for _, codec := range profile.CodecProfiles {
		if !boundedScalar(codec.Type) || !boundedList(codec.Codec) || len(codec.Conditions) > 32 {
			return false
		}
		for _, condition := range codec.Conditions {
			if !boundedScalar(condition.Condition) || !boundedScalar(condition.Property) || !boundedScalar(condition.Value) {
				return false
			}
		}
	}
	for _, container := range profile.ContainerProfiles {
		if !boundedScalar(container.Type) || !boundedList(container.Container) || !boundedList(container.SubContainer) || len(container.Conditions) > 32 {
			return false
		}
		for _, condition := range container.Conditions {
			if !boundedScalar(condition.Condition) || !boundedScalar(condition.Property) || !boundedScalar(condition.Value) {
				return false
			}
		}
	}
	for _, direct := range profile.DirectPlayProfiles {
		if !boundedList(direct.Container) || !boundedList(direct.AudioCodec) || !boundedList(direct.VideoCodec) || !boundedScalar(direct.Type) {
			return false
		}
	}
	for _, transcode := range profile.TranscodingProfiles {
		if !boundedList(transcode.Container) || !boundedList(transcode.AudioCodec) || !boundedList(transcode.VideoCodec) ||
			!boundedScalar(transcode.Type) || !boundedScalar(transcode.Protocol) || !boundedScalar(transcode.Context) || !boundedScalar(transcode.MaxAudioChannels) {
			return false
		}
		if _, err := transcodingProfileMaxAudioChannels(transcode.MaxAudioChannels); err != nil {
			return false
		}
	}
	for _, subtitle := range profile.SubtitleProfiles {
		if !boundedList(subtitle.Format) || !boundedScalar(subtitle.Method) {
			return false
		}
	}
	if _, err := containerProfileConstraints(profile.ContainerProfiles); err != nil {
		return false
	}
	return true
}

func containerProfileConstraints(profiles []ContainerProfile) ([]playback.ContainerProfile, error) {
	result := make([]playback.ContainerProfile, 0, len(profiles))
	for _, profile := range profiles {
		switch normalizeCapability(profile.Type) {
		case "":
			continue
		case "video":
		case "audio", "photo", "subtitle", "lyric":
			continue
		default:
			return nil, ErrInvalidQuery
		}
		containers, err := capabilityList(profile.Container)
		if err != nil || len(containers) == 0 {
			return nil, ErrInvalidQuery
		}
		normalized := make([]string, 0, len(containers))
		for _, container := range containers {
			appendUnique(&normalized, normalizedContainer(container))
		}
		if strings.TrimSpace(profile.SubContainer) != "" {
			if _, err := capabilityList(profile.SubContainer); err != nil {
				return nil, ErrInvalidQuery
			}
		}
		conditions := make([]playback.ProfileCondition, 0, len(profile.Conditions))
		for _, condition := range profile.Conditions {
			if err := validateContainerProfileCondition(condition); err != nil {
				return nil, err
			}
			conditions = append(conditions, playback.ProfileCondition{
				Condition: normalizeCapability(condition.Condition),
				Property:  normalizeCapability(condition.Property),
				Value:     strings.TrimSpace(condition.Value),
				Required:  condition.IsRequired,
			})
		}
		result = append(result, playback.ContainerProfile{ContainersCSV: strings.Join(normalized, ","), Conditions: conditions})
	}
	return result, nil
}

func validateContainerProfileCondition(condition ProfileCondition) error {
	operator := normalizeCapability(condition.Condition)
	property := normalizeCapability(condition.Property)
	value := strings.TrimSpace(condition.Value)
	numeric := false
	switch property {
	case "width", "height", "videolevel", "videobitrate", "numstreams", "numvideostreams", "numaudiostreams":
		numeric = true
	case "videoprofile", "videorangetype":
	default:
		return ErrInvalidQuery
	}
	switch operator {
	case "equals", "notequals":
	case "lessthanequal", "greaterthanequal":
		if !numeric {
			return ErrInvalidQuery
		}
	case "equalsany":
	default:
		return ErrInvalidQuery
	}
	values := []string{value}
	if operator == "equalsany" {
		values = strings.Split(value, "|")
		if len(values) == 0 || len(values) > 32 {
			return ErrInvalidQuery
		}
	}
	for _, candidate := range values {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || len(candidate) > 64 {
			return ErrInvalidQuery
		}
		if numeric {
			number, err := strconv.ParseInt(candidate, 10, 64)
			if err != nil || number < 0 {
				return ErrInvalidQuery
			}
		}
	}
	return nil
}

func codecProfileConstraints(profiles []CodecProfile) (map[string]playback.MediaProfile, error) {
	constraints := make(map[string]playback.MediaProfile)
	for _, profile := range profiles {
		codecs, err := capabilityList(profile.Codec)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(strings.TrimSpace(profile.Type), "Video") {
			continue
		}
		for _, codec := range codecs {
			key := codecConstraintKey(codec)
			constraint := constraints[key]
			for _, condition := range profile.Conditions {
				property := strings.ToLower(strings.TrimSpace(condition.Property))
				operator := strings.ToLower(strings.TrimSpace(condition.Condition))
				switch {
				case property == "videolevel" && operator == "lessthanequal":
					level, parseErr := strconv.Atoi(strings.TrimSpace(condition.Value))
					if parseErr != nil || level < 1 || level > 1000 {
						constraint.RequiredConditionUnknown = constraint.RequiredConditionUnknown || condition.IsRequired
						continue
					}
					if constraint.MaximumVideoLevel == 0 || level < constraint.MaximumVideoLevel {
						constraint.MaximumVideoLevel = level
					}
					constraint.VideoLevelRequired = constraint.VideoLevelRequired || condition.IsRequired
				case property == "videorangetype" && operator == "notequals":
					videoRange := strings.ToUpper(strings.TrimSpace(condition.Value))
					if videoRange == "" || constraint.ExcludedVideoRange != "" && !strings.EqualFold(constraint.ExcludedVideoRange, videoRange) {
						constraint.RequiredConditionUnknown = constraint.RequiredConditionUnknown || condition.IsRequired
						continue
					}
					constraint.ExcludedVideoRange = videoRange
					constraint.VideoRangeRequired = constraint.VideoRangeRequired || condition.IsRequired
					if videoRange == "DOVI" {
						constraint.SupportsNonDolbyVisionHDR = true
					}
				default:
					constraint.RequiredConditionUnknown = constraint.RequiredConditionUnknown || condition.IsRequired
				}
			}
			constraints[key] = constraint
		}
	}
	return constraints, nil
}

func codecConstraintKey(codec string) string {
	switch normalizeCapability(codec) {
	case "hevc", "h265", "h.265", "hev1", "hvc1":
		return "h265"
	case "avc", "avc1", "h.264", "x264":
		return "h264"
	default:
		return normalizeCapability(codec)
	}
}

func playbackCapabilities(input PlaybackInfoRequest) (playback.Capabilities, bool, error) {
	profile := input.DeviceProfile
	if !validDeviceProfileBounds(profile) {
		return playback.Capabilities{}, false, ErrInvalidQuery
	}
	directPlay := playbackFlag(input.EnableDirectPlay, true)
	directStream := playbackFlag(input.EnableDirectStream, true)
	enableTranscoding := playbackFlag(input.EnableTranscoding, true)
	allowVideoCopy := playbackFlag(input.AllowVideoStreamCopy, true)
	allowAudioCopy := playbackFlag(input.AllowAudioStreamCopy, true)
	capabilities := playback.Capabilities{MaximumAudioChannels: input.MaxAudioChannels}
	capabilities.PreferDirectPlay = &directPlay
	codecConstraints, err := codecProfileConstraints(profile.CodecProfiles)
	if err != nil {
		return playback.Capabilities{}, false, err
	}
	containerConstraints, err := containerProfileConstraints(profile.ContainerProfiles)
	if err != nil {
		return playback.Capabilities{}, false, err
	}
	capabilities.ContainerProfiles = containerConstraints
	profiles := make(map[string]struct{})
	addProfile := func(containers []string, video string, audios []string, direct bool) error {
		video = normalizeCapability(video)
		if len(containers) == 0 || video == "" {
			return nil
		}
		normalizedContainers := make([]string, 0, len(containers))
		for _, container := range containers {
			appendUnique(&normalizedContainers, normalizedContainer(container))
		}
		normalizedAudios := make([]string, 0, len(audios))
		for _, audio := range audios {
			appendUnique(&normalizedAudios, normalizeCapability(audio))
		}
		containerList, audioList := strings.Join(normalizedContainers, ","), strings.Join(normalizedAudios, ",")
		key := strconv.FormatBool(direct) + "\x00" + containerList + "\x00" + video + "\x00" + audioList
		if _, exists := profiles[key]; exists {
			return nil
		}
		if len(profiles) >= 32 {
			return ErrInvalidQuery
		}
		mediaProfile := playback.MediaProfile{}
		if direct {
			mediaProfile = codecConstraints[codecConstraintKey(video)]
			mediaProfile.DirectPlay = true
		} else {
			mediaProfile.Transcoding = true
		}
		mediaProfile.Container, mediaProfile.VideoCodec = normalizedContainers[0], video
		mediaProfile.ContainersCSV = containerList
		if len(normalizedAudios) > 0 {
			mediaProfile.AudioCodec = normalizedAudios[0]
			mediaProfile.AudioCodecsCSV = audioList
		}
		profiles[key] = struct{}{}
		capabilities.MediaProfiles = append(capabilities.MediaProfiles, mediaProfile)
		return nil
	}
	if directPlay || directStream {
		for _, direct := range profile.DirectPlayProfiles {
			if direct.Type != "" && !strings.EqualFold(direct.Type, "Video") {
				continue
			}
			containers, err := capabilityList(direct.Container)
			if err != nil {
				return playback.Capabilities{}, false, err
			}
			videos, err := capabilityList(direct.VideoCodec)
			if err != nil {
				return playback.Capabilities{}, false, err
			}
			audios, err := capabilityList(direct.AudioCodec)
			if err != nil {
				return playback.Capabilities{}, false, err
			}
			for index, container := range containers {
				if container == "m3u8" || container == "hls" {
					appendUnique(&capabilities.StreamingProtocols, "hls")
				} else {
					appendUnique(&capabilities.StreamingProtocols, "http")
				}
				containers[index] = normalizedContainer(container)
				appendUnique(&capabilities.Containers, containers[index])
			}
			for _, audio := range audios {
				appendUnique(&capabilities.AudioCodecs, audio)
			}
			for _, video := range videos {
				appendUnique(&capabilities.VideoCodecs, video)
				if err := addProfile(containers, video, audios, true); err != nil {
					return playback.Capabilities{}, false, err
				}
			}
		}
	}
	if directStream && allowVideoCopy && allowAudioCopy {
		appendUnique(&capabilities.ProcessingModes, "remux")
	}
	allowTranscode := false
	if directStream || enableTranscoding {
		for _, transcode := range profile.TranscodingProfiles {
			segmentContainer, supported, err := hlsTranscodingProfileSegmentContainer(transcode)
			if err != nil {
				return playback.Capabilities{}, false, err
			}
			if supported && (capabilities.HLSSegmentContainer == "" || segmentContainer == "ts") {
				capabilities.HLSSegmentContainer = segmentContainer
			}
		}
		for _, transcode := range profile.TranscodingProfiles {
			segmentContainer, supported, err := hlsTranscodingProfileSegmentContainer(transcode)
			if err != nil {
				return playback.Capabilities{}, false, err
			}
			if !supported || segmentContainer != capabilities.HLSSegmentContainer {
				continue
			}
			videos, err := capabilityList(transcode.VideoCodec)
			if err != nil {
				return playback.Capabilities{}, false, err
			}
			audios, err := capabilityList(transcode.AudioCodec)
			if err != nil {
				return playback.Capabilities{}, false, err
			}
			if _, err := transcodingProfileMaxAudioChannels(transcode.MaxAudioChannels); err != nil {
				return playback.Capabilities{}, false, err
			}
			appendUnique(&capabilities.StreamingProtocols, "hls")
			for _, video := range videos {
				if err := addProfile([]string{"mp4"}, video, audios, false); err != nil {
					return playback.Capabilities{}, false, err
				}
				for _, audio := range audios {
					if enableTranscoding && video == "h264" && audio == "aac" {
						appendUnique(&capabilities.ProcessingModes, "transcode")
						if allowVideoCopy {
							appendUnique(&capabilities.ProcessingModes, "transcode_audio")
						}
						allowTranscode = true
					}
					if directStream && allowVideoCopy && allowAudioCopy {
						appendUnique(&capabilities.ProcessingModes, "remux")
					}
				}
			}
		}
	}
	for _, subtitle := range profile.SubtitleProfiles {
		if len(subtitle.Format) > 64 || len(subtitle.Method) > 64 {
			return playback.Capabilities{}, false, ErrInvalidQuery
		}
		switch strings.ToLower(strings.TrimSpace(subtitle.Method)) {
		case "external", "embed":
			appendUnique(&capabilities.SubtitleModes, "external")
		case "encode", "burn":
			appendUnique(&capabilities.SubtitleModes, "burn")
		}
	}
	bitrate, err := effectiveBitrate(input.MaxStreamingBitrate, profile.MaxStreamingBitrate)
	if err != nil {
		return playback.Capabilities{}, false, err
	}
	capabilities.MaximumVideoBitrateKbps = bitrate
	capabilities.TranscodeVideoBitrateKbps = bitrate
	if len(capabilities.MediaProfiles) == 0 || len(capabilities.StreamingProtocols) == 0 {
		return playback.Capabilities{}, false, playback.ErrClientCapabilityMissing
	}
	return capabilities, allowTranscode, nil
}

func hlsTranscodingProfileSegmentContainer(profile TranscodingProfile) (string, bool, error) {
	if profile.Type != "" && !strings.EqualFold(profile.Type, "Video") ||
		profile.Context != "" && !strings.EqualFold(profile.Context, "Streaming") ||
		normalizeCapability(profile.Protocol) != "hls" {
		return "", false, nil
	}
	containers, err := capabilityList(profile.Container)
	if err != nil {
		return "", false, err
	}
	segmentContainer := ""
	for _, container := range containers {
		switch normalizedContainer(container) {
		case "mp4":
			if segmentContainer == "" {
				segmentContainer = "mp4"
			}
		case "hls", "ts", "mpegts":
			segmentContainer = "ts"
		}
	}
	return segmentContainer, segmentContainer != "", nil
}

func transcodingProfileMaxAudioChannels(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	channels, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || channels < 1 || channels > 32 {
		return 0, ErrInvalidQuery
	}
	return channels, nil
}

func usableSourceOptions(options []playback.SourceOption, capabilities playback.Capabilities, allowTranscode bool) []playback.SourceOption {
	result := make([]playback.SourceOption, 0, len(options))
	for _, option := range options {
		protocol := normalizeCapability(option.Protocol)
		if protocol == "external" || protocol == "youtube" || option.SourceRef == "" {
			continue
		}
		direct := containsFold(capabilities.StreamingProtocols, protocol) &&
			(protocol == "hls" || option.Container == "" || containsFold(capabilities.Containers, normalizedContainer(option.Container)))
		if protocol != "http" && protocol != "hls" || !direct && !allowTranscode && !supportsHLSRemux(capabilities) {
			continue
		}
		result = append(result, option)
	}
	return result
}

func supportsHLSRemux(capabilities playback.Capabilities) bool {
	return containsFold(capabilities.ProcessingModes, "remux") && containsFold(capabilities.StreamingProtocols, "hls")
}

func sourceOptionReferenceIDs(options []playback.SourceOption) []string {
	references := make([]string, 0, len(options))
	for _, option := range options {
		if option.SourceRef != "" {
			references = append(references, option.SourceRef)
		}
	}
	return references
}

func mediaSourceDTO(item watchstate.CatalogTitle, playID string, descriptor playSourceDescriptor, capabilities playback.Capabilities, allowTranscode bool, startTimeTicks int64) MediaSourceInfo {
	values := compatibilityPlaybackValues(playID, descriptor.ID, startTimeTicks)
	container := normalizedContainer(descriptor.Container)
	pathContainer := container
	protocol := normalizeCapability(descriptor.Protocol)
	if protocol == "hls" || pathContainer == "hls" {
		pathContainer = "m3u8"
	}
	streamPath := compatibilityStreamPath(item.ID, pathContainer, values)
	direct := containsFold(capabilities.StreamingProtocols, protocol) &&
		(protocol == "hls" || container == "" || containsFold(capabilities.Containers, container))
	supportsDirectPlay := direct && playbackFlag(capabilities.PreferDirectPlay, true)
	supportsDirectStream := containsFold(capabilities.ProcessingModes, "remux") && (direct || supportsHLSRemux(capabilities))
	formats := []string{}
	if container != "" {
		formats = append(formats, container)
	}
	source := MediaSourceInfo{
		Id: descriptor.ID, Name: descriptor.Name, Path: compatibilityMediaPath(item.ID, descriptor.ID, pathContainer), Container: container,
		Protocol: "File", Type: "Default", IsRemote: false, SupportsDirectPlay: supportsDirectPlay,
		SupportsDirectStream: supportsDirectStream, SupportsTranscoding: allowTranscode, SupportsProbing: true, VideoType: "VideoFile",
		Formats: formats, RequiredHttpHeaders: map[string]string{}, MediaAttachments: []any{}, MediaStreams: []MediaStreamInfo{},
		RunTimeTicks: catalogRuntimeTicks(item), ETag: item.ID,
	}
	if supportsDirectPlay || supportsDirectStream {
		if !direct && supportsHLSRemux(capabilities) {
			source.DirectStreamUrl = compatibilityMasterPath(item.ID, values)
		} else {
			source.DirectStreamUrl = streamPath
		}
	}
	if allowTranscode {
		segmentContainer := capabilities.HLSSegmentContainer
		if segmentContainer != "mp4" {
			segmentContainer = "ts"
		}
		source.TranscodingUrl = compatibilityMasterPath(item.ID, values)
		source.TranscodingSubProtocol = "hls"
		source.TranscodingContainer = segmentContainer
	}
	return source
}

func compatibilityStreamPath(itemID, container string, values url.Values) string {
	container = strings.ToLower(strings.TrimSpace(container))
	if !validContainer(container) {
		container = "mp4"
	}
	return "/Videos/" + url.PathEscape(itemID) + "/stream." + container + "?" + values.Encode()
}

func compatibilityMediaPath(itemID, mediaID, container string) string {
	container = strings.ToLower(strings.TrimSpace(container))
	if !validContainer(container) {
		container = "mp4"
	}
	return "/rivune/" + url.PathEscape(itemID) + "/" + url.PathEscape(mediaID) + "." + container
}

func compatibilityMasterPath(itemID string, values url.Values) string {
	return "/Videos/" + url.PathEscape(itemID) + "/master.m3u8?" + values.Encode()
}

func compatibilityPlaybackValues(playID, mediaID string, startTimeTicks int64) url.Values {
	values := url.Values{"MediaSourceId": {mediaID}, "Static": {"true"}}
	if playID != "" {
		values.Set("PlaySessionId", playID)
		values.Set("api_key", playID)
	}
	if startTimeTicks > 0 {
		values.Set("StartTimeTicks", strconv.FormatInt(startTimeTicks, 10))
	}
	return values
}

func selectedPlaybackSource(session playback.Session) (playback.Source, bool) {
	for _, source := range session.Sources {
		if source.ID == session.SelectedSourceID {
			return source, true
		}
	}
	return playback.Source{}, false
}

func legacyStreamRequest(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	path := strings.ToLower(request.URL.Path)
	return strings.Contains(path, "/stream") && !strings.HasSuffix(path, ".m3u8") && !strings.Contains(path, "/hls1/")
}

func legacyStreamFallbackAllowed(request *http.Request, playID string) bool {
	if request == nil || request.URL == nil || isRivunePlaySessionID(playID) {
		return false
	}
	static, err := booleanValue(request.URL.Query(), "Static", false)
	return err == nil && (static || legacyStreamRequest(request))
}

func legacyProcessingStreamRequest(request *http.Request, session playback.Session) bool {
	if !legacyStreamRequest(request) {
		return false
	}
	selected, ok := selectedPlaybackSource(session)
	if !ok || normalizeCapability(selected.Protocol) != "hls" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(selected.Mode)) {
	case "remux", "transcode_audio", "transcode":
		return true
	default:
		return false
	}
}

func catalogRuntimeTicks(item watchstate.CatalogTitle) *int64 {
	if item.RuntimeMinutes == nil || *item.RuntimeMinutes <= 0 {
		return nil
	}
	value := MinutesToTicks(int64(*item.RuntimeMinutes))
	return &value
}

func applyResolvedMediaSource(sources []MediaSourceInfo, itemID, mediaID, playID string, startTimeTicks int64, session playback.Session) {
	selected, ok := selectedPlaybackSource(session)
	if !ok {
		return
	}
	for index := range sources {
		if sources[index].Id != mediaID {
			continue
		}
		reconcileResolvedMediaSource(&sources[index], itemID, mediaID, playID, startTimeTicks, selected)
		if selected.Media != nil {
			sources[index].Name = verifiedCompatibilitySourceName(sources[index].Name, selected.Media, session.SelectedAudioTrack)
			sources[index].MediaStreams, sources[index].DefaultAudioStreamIndex = compatibilityMediaStreams(selected.Media, session.SelectedAudioTrack)
			sources[index].MediaStreams = applyCompatibilitySubtitleDelivery(
				sources[index].MediaStreams, session.Subtitles, itemID, mediaID, playID, startTimeTicks,
			)
			if selected.Media.DurationSeconds > 0 {
				ticks := SecondsToTicks(int64(selected.Media.DurationSeconds))
				sources[index].RunTimeTicks = &ticks
			}
			var kbps int64
			for _, track := range append(append([]playback.MediaTrack(nil), selected.Media.VideoTracks...), selected.Media.AudioTracks...) {
				if track.BitrateKbps > 0 {
					kbps += int64(track.BitrateKbps)
				}
			}
			if kbps > 0 && kbps <= (1<<63-1)/1000 {
				value := kbps * 1000
				sources[index].Bitrate = &value
			}
		}
		return
	}
}

func promoteSelectedMediaSource(sources []MediaSourceInfo, mediaID string) {
	for index := range sources {
		if sources[index].Id != mediaID || index == 0 {
			continue
		}
		selected := sources[index]
		copy(sources[1:index+1], sources[:index])
		sources[0] = selected
		return
	}
}

func reconcileResolvedMediaSource(source *MediaSourceInfo, itemID, mediaID, playID string, startTimeTicks int64, native playback.Source) {
	if source == nil {
		return
	}
	segmentContainer := normalizeCapability(source.TranscodingContainer)
	source.DirectStreamUrl = ""
	source.TranscodingUrl = ""
	source.TranscodingSubProtocol = ""
	source.TranscodingContainer = ""
	source.SupportsDirectPlay = false
	source.SupportsDirectStream = false
	source.SupportsTranscoding = false

	container := normalizedContainer(native.Container)
	pathContainer := container
	if normalizeCapability(native.Protocol) == "hls" || pathContainer == "hls" {
		pathContainer = "m3u8"
	}
	source.Container = container
	if container != "" {
		source.Formats = []string{container}
	}
	source.Path = compatibilityMediaPath(itemID, mediaID, pathContainer)
	values := compatibilityPlaybackValues(playID, mediaID, startTimeTicks)
	streamPath := compatibilityStreamPath(itemID, pathContainer, values)
	masterPath := compatibilityMasterPath(itemID, values)
	switch strings.ToLower(strings.TrimSpace(native.Mode)) {
	case "direct":
		source.SupportsDirectPlay = true
		source.SupportsDirectStream = true
		source.DirectStreamUrl = streamPath
	case "remux":
		source.SupportsDirectStream = true
		source.DirectStreamUrl = masterPath
	case "transcode_audio", "transcode":
		source.SupportsTranscoding = true
		source.TranscodingUrl = masterPath
		source.TranscodingSubProtocol = "hls"
		if segmentContainer == "mp4" {
			source.TranscodingContainer = "mp4"
		} else {
			source.TranscodingContainer = "ts"
		}
	}
}

func verifiedCompatibilitySourceName(base string, media *playback.MediaInspection, selectedAudioIndex *int) string {
	if media == nil {
		return base
	}
	if separator := strings.Index(base, " · "); separator >= 0 {
		base = base[:separator]
	}
	parts := []string{base}
	if len(media.VideoTracks) != 0 && media.VideoTracks[0].Height > 0 && media.VideoTracks[0].Height <= 16384 {
		parts = append(parts, strconv.Itoa(media.VideoTracks[0].Height)+"p")
	}
	if len(media.VideoTracks) != 0 {
		if _, display := compatibilityCodec(media.VideoTracks[0].Codec); display != "" {
			parts = append(parts, display)
		}
	}
	if audio := selectedCompatibilityTrack(media.AudioTracks, selectedAudioIndex); audio != nil {
		if _, display := compatibilityCodec(audio.Codec); display != "" {
			parts = append(parts, display)
		}
	}
	if display := compatibilityHDR(media.HDRFormat); display != "" {
		parts = append(parts, display)
	}
	return strings.Join(parts, " · ")
}

func selectedCompatibilityTrack(tracks []playback.MediaTrack, selectedIndex *int) *playback.MediaTrack {
	if selectedIndex != nil {
		for index := range tracks {
			if tracks[index].Index == *selectedIndex {
				return &tracks[index]
			}
		}
	}
	if len(tracks) == 0 {
		return nil
	}
	return &tracks[0]
}

func compatibilityMediaStreams(media *playback.MediaInspection, selectedAudioIndex *int) ([]MediaStreamInfo, *int) {
	if media == nil {
		return []MediaStreamInfo{}, nil
	}
	selectedAudio := selectedCompatibilityTrack(media.AudioTracks, selectedAudioIndex)
	var defaultAudio *int
	if selectedAudio != nil {
		value := selectedAudio.Index
		defaultAudio = &value
	}
	streams := make([]MediaStreamInfo, 0, min(maximumCompatibilityMediaStreams, len(media.VideoTracks)+len(media.AudioTracks)+len(media.SubtitleTracks)))
	appendTracks := func(tracks []playback.MediaTrack, mediaType string) {
		for _, track := range tracks {
			if len(streams) >= maximumCompatibilityMediaStreams || track.Index < 0 {
				break
			}
			codec, display := compatibilityCodec(track.Codec)
			stream := MediaStreamInfo{Codec: codec, Language: compatibilityLanguage(track.Language), DisplayTitle: display, Type: mediaType, Index: track.Index, IsForced: track.Forced}
			if mediaType == "Audio" && defaultAudio != nil && track.Index == *defaultAudio {
				stream.IsDefault = true
			}
			if track.Width > 0 && track.Width <= 16384 {
				stream.Width = track.Width
			}
			if track.Height > 0 && track.Height <= 16384 {
				stream.Height = track.Height
			}
			if track.Channels > 0 && track.Channels <= 64 {
				stream.Channels = track.Channels
			}
			if track.BitrateKbps > 0 && int64(track.BitrateKbps) <= (1<<63-1)/1000 {
				stream.BitRate = int64(track.BitrateKbps) * 1000
			}
			streams = append(streams, stream)
		}
	}
	appendTracks(media.VideoTracks, "Video")
	appendTracks(media.AudioTracks, "Audio")
	appendTracks(media.SubtitleTracks, "Subtitle")
	return streams, defaultAudio
}

type compatibilitySubtitleBinding struct {
	index    int
	assetID  string
	subtitle playback.Subtitle
}

func applyCompatibilitySubtitleDelivery(streams []MediaStreamInfo, subtitles []playback.Subtitle, itemID, mediaID, playID string, startTimeTicks int64) []MediaStreamInfo {
	bindings := compatibilitySubtitleBindings(streams, subtitles)
	for _, binding := range bindings {
		var stream *MediaStreamInfo
		for streamIndex := range streams {
			if streams[streamIndex].Type == "Subtitle" && streams[streamIndex].Index == binding.index {
				stream = &streams[streamIndex]
				break
			}
		}
		if stream == nil {
			if len(streams) >= maximumCompatibilityMediaStreams {
				break
			}
			streams = append(streams, MediaStreamInfo{
				Codec: "webvtt", DisplayTitle: "WEBVTT", Type: "Subtitle", Index: binding.index,
				Language: compatibilityLanguage(binding.subtitle.Language), IsForced: binding.subtitle.Forced,
			})
			stream = &streams[len(streams)-1]
		}
		stream.Codec = "webvtt"
		stream.IsExternal = true
		stream.IsExternalUrl = false
		stream.IsTextSubtitleStream = true
		stream.SupportsExternalStream = true
		stream.DeliveryMethod = "External"
		stream.DeliveryUrl = compatibilitySubtitleDeliveryURL(itemID, mediaID, binding.index, playID, startTimeTicks)
	}
	return streams
}

func compatibilitySubtitleBindings(streams []MediaStreamInfo, subtitles []playback.Subtitle) []compatibilitySubtitleBinding {
	occupied := make(map[int]struct{}, len(streams))
	next := 0
	for _, stream := range streams {
		if stream.Index < 0 || stream.Index > maximumCompatibilitySubtitleIndex {
			continue
		}
		occupied[stream.Index] = struct{}{}
		if stream.Index >= next {
			next = stream.Index + 1
		}
	}
	bindings := make([]compatibilitySubtitleBinding, 0, min(len(subtitles), maximumCompatibilityMediaStreams))
	providerSlots := maximumCompatibilityMediaStreams - len(streams)
	if providerSlots < 0 {
		providerSlots = 0
	}
	for _, subtitle := range subtitles {
		if subtitle.Delivery != "external" || subtitle.ID == "" {
			continue
		}
		if strings.HasPrefix(subtitle.ID, "embedded-subtitle-") {
			index, err := strconv.Atoi(strings.TrimPrefix(subtitle.ID, "embedded-subtitle-"))
			if err != nil || index < 0 || index > maximumCompatibilitySubtitleIndex {
				continue
			}
			for _, stream := range streams {
				if stream.Type == "Subtitle" && stream.Index == index {
					bindings = append(bindings, compatibilitySubtitleBinding{index: index, assetID: subtitle.ID, subtitle: subtitle})
					break
				}
			}
			continue
		}
		if providerSlots == 0 {
			continue
		}
		for next <= maximumCompatibilitySubtitleIndex {
			if _, exists := occupied[next]; !exists {
				break
			}
			next++
		}
		if next > maximumCompatibilitySubtitleIndex {
			break
		}
		bindings = append(bindings, compatibilitySubtitleBinding{index: next, assetID: subtitle.ID, subtitle: subtitle})
		occupied[next] = struct{}{}
		next++
		providerSlots--
	}
	return bindings
}

func compatibilitySubtitleDeliveryURL(itemID, mediaID string, index int, playID string, startTimeTicks int64) string {
	values := url.Values{"PlaySessionId": {playID}, "MediaSourceId": {mediaID}, "api_key": {playID}}
	if startTimeTicks > 0 {
		values.Set("StartTimeTicks", strconv.FormatInt(startTimeTicks, 10))
	}
	return "/Videos/" + url.PathEscape(itemID) + "/" + url.PathEscape(mediaID) + "/Subtitles/" + strconv.Itoa(index) + "/Stream.vtt?" + values.Encode()
}

func compatibilityCodec(value string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "h264", "avc", "avc1", "h.264", "x264":
		return "h264", "H.264"
	case "h265", "hevc", "hev1", "hvc1", "h.265", "x265":
		return "hevc", "HEVC"
	case "av1", "av01":
		return "av1", "AV1"
	case "vp9", "vp09":
		return "vp9", "VP9"
	case "aac", "mp4a", "mp4a.40.2":
		return "aac", "AAC"
	case "ac3":
		return "ac3", "AC-3"
	case "eac3", "e-ac-3", "eac-3":
		return "eac3", "E-AC-3"
	case "opus":
		return "opus", "Opus"
	case "dts":
		return "dts", "DTS"
	case "truehd":
		return "truehd", "TrueHD"
	case "flac":
		return "flac", "FLAC"
	case "mp3":
		return "mp3", "MP3"
	case "srt", "subrip":
		return "srt", "SRT"
	case "ass", "ssa":
		return "ass", "ASS"
	case "webvtt", "vtt":
		return "webvtt", "WebVTT"
	case "hdmv_pgs_subtitle", "pgs":
		return "pgs", "PGS"
	default:
		return "", ""
	}
}

func compatibilityHDR(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "hdr10":
		return "HDR10"
	case "hlg":
		return "HLG"
	case "dolby_vision":
		return "Dolby Vision"
	default:
		return ""
	}
}

func compatibilityLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 12 {
		return ""
	}
	for _, character := range value {
		if character < 'a' || character > 'z' {
			return ""
		}
	}
	return value
}

func parseStreamSelectors(values url.Values) (string, string, int64, error) {
	playID, found, err := queryScalar(values, "PlaySessionId")
	if err != nil || !found || len(playID) < 16 || len(playID) > 128 {
		return "", "", 0, errPlaySessionNotFound
	}
	mediaID, found, err := queryScalar(values, "MediaSourceId")
	if err != nil || !found || len(mediaID) < 16 || len(mediaID) > 128 {
		return "", "", 0, errPlaySessionNotFound
	}
	if canonicalMediaID, valid := canonicalCompatUUID(mediaID); valid {
		mediaID = canonicalMediaID
	}
	var ticks int64
	if raw, found, scalarErr := queryScalar(values, "StartTimeTicks"); scalarErr != nil {
		return "", "", 0, scalarErr
	} else if found {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || parsed < 0 {
			return "", "", 0, errPlaySessionNotFound
		}
		ticks = parsed
	}
	if ticks > 7*24*60*60*TicksPerSecond {
		return "", "", 0, errPlaySessionNotFound
	}
	return playID, mediaID, ticks, nil
}

func preferredPlaybackResource(providerIDs map[string]string) string {
	for _, key := range []string{"imdb", "tmdb", "tvdb"} {
		for actual, value := range providerIDs {
			if strings.EqualFold(actual, key) && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func effectiveBitrate(requested, profile int64) (int, error) {
	value := requested
	if value <= 0 || profile > 0 && profile < value {
		value = profile
	}
	if value == 0 {
		return 0, nil
	}
	if value < 64_000 || value > maximumCompatClientBitrate {
		return 0, ErrInvalidQuery
	}
	if value > maximumCompatPlaybackBitrate {
		value = maximumCompatPlaybackBitrate
	}
	return int((value + 999) / 1000), nil
}

func capabilityList(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 32 {
		return nil, ErrInvalidQuery
	}
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = normalizeCapability(part)
		if part == "" || len(part) > 64 {
			return nil, ErrInvalidQuery
		}
		appendUnique(&result, part)
	}
	return result, nil
}

func normalizeCapability(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizedContainer(value string) string {
	switch normalizeCapability(value) {
	case "m4v", "mov":
		return "mp4"
	case "m3u8":
		return "hls"
	default:
		return normalizeCapability(value)
	}
}

func appendUnique(values *[]string, value string) {
	if value == "" || containsFold(*values, value) {
		return
	}
	*values = append(*values, value)
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

func (handler *Handler) writePlaybackError(response http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, watchstate.ErrNotFound), errors.Is(err, errPlaySessionNotFound), errors.Is(err, playback.ErrSessionNotFound), errors.Is(err, playback.ErrSourceReferenceExpired), errors.Is(err, playback.ErrNoPlayableSource):
		handler.writeCompatError(response, http.StatusNotFound, "PlaybackSessionNotFound", "The playback session or item is invalid or expired")
	case errors.Is(err, playback.ErrInvalidInput):
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The playback request is invalid")
	case errors.Is(err, playback.ErrUnsupportedSource), errors.Is(err, playback.ErrTranscodingDisabled), errors.Is(err, playback.ErrClientCapabilityMissing):
		handler.writeCompatError(response, http.StatusUnprocessableEntity, "PlaybackProfileUnsupported", "The selected source is unsupported by this device")
	case errors.Is(err, playback.ErrProviderUnavailable), errors.Is(err, playback.ErrMediaSourceFailed):
		handler.writeCompatError(response, http.StatusBadGateway, "PlaybackSourceFailed", "The media source is unavailable")
	case errors.Is(err, playback.ErrMediaCapacityReached):
		response.Header().Set("Retry-After", "10")
		handler.writeCompatError(response, http.StatusServiceUnavailable, "PlaybackCapacityReached", "Playback capacity is temporarily unavailable")
	case errors.Is(err, playback.ErrMediaStorageLimit):
		handler.writeCompatError(response, http.StatusInsufficientStorage, "PlaybackStorageLimit", "Playback storage is temporarily unavailable")
	case errors.Is(err, request.Context().Err()):
		return
	default:
		handler.writeCompatError(response, http.StatusInternalServerError, "InternalError", "The playback request failed")
	}
}

func (handler *Handler) writeStreamPlaybackError(response http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, watchstate.ErrNotFound), errors.Is(err, errPlaySessionNotFound), errors.Is(err, playback.ErrSessionNotFound), errors.Is(err, playback.ErrSourceReferenceExpired):
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The playback session is invalid or expired")
	case errors.Is(err, playback.ErrUnsupportedSource), errors.Is(err, playback.ErrTranscodingDisabled), errors.Is(err, playback.ErrClientCapabilityMissing), errors.Is(err, playback.ErrNoPlayableSource):
		handler.writeStreamError(response, request, http.StatusUnprocessableEntity, "PlaybackProfileUnsupported", "The selected source is unsupported by this device")
	case errors.Is(err, playback.ErrProviderUnavailable), errors.Is(err, playback.ErrMediaSourceFailed):
		handler.writeStreamError(response, request, http.StatusBadGateway, "PlaybackSourceFailed", "The media source is unavailable")
	case errors.Is(err, playback.ErrMediaCapacityReached):
		response.Header().Set("Retry-After", "10")
		handler.writeStreamError(response, request, http.StatusServiceUnavailable, "PlaybackCapacityReached", "Playback capacity is temporarily unavailable")
	case errors.Is(err, playback.ErrMediaStorageLimit):
		handler.writeStreamError(response, request, http.StatusInsufficientStorage, "PlaybackStorageLimit", "Playback storage is temporarily unavailable")
	case errors.Is(err, request.Context().Err()):
		return
	default:
		handler.writeStreamError(response, request, http.StatusInternalServerError, "InternalError", "The playback request failed")
	}
}

func (handler *Handler) writeStreamError(response http.ResponseWriter, request *http.Request, status int, code, message string) {
	if request.Method == http.MethodHead {
		response.WriteHeader(status)
		return
	}
	handler.writeCompatError(response, status, code, message)
}
