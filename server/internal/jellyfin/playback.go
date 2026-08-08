package jellyfin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/moodiness/rivune/server/internal/playback"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

const (
	maximumPlaybackInfoBodyBytes = 256 << 10
	// Keep one admission beyond the 256-session registry below the 4096-reference global store limit.
	maximumCompatibilityMediaSources = 15
)

func (handler *Handler) handlePlaybackInfo(response http.ResponseWriter, request *http.Request) {
	if handler.playback == nil || handler.catalog == nil || handler.playSessions == nil {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, false)
	if !ok {
		return
	}
	input, err := parsePlaybackInfoRequest(response, request)
	if err != nil {
		handler.writeCompatError(response, http.StatusBadRequest, "InvalidRequest", "The playback request is invalid")
		return
	}
	if input.UserId != "" && !strings.EqualFold(input.UserId, session.ProfileID) {
		handler.writeCompatError(response, http.StatusNotFound, "ItemNotFound", "The item was not found")
		return
	}
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, request.PathValue("id"))
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
			result := PlaybackInfoResponse{PlaySessionId: playID, MediaSources: make([]MediaSourceInfo, 0, len(descriptors))}
			for _, descriptor := range descriptors {
				result.MediaSources = append(result.MediaSources, mediaSourceDTO(item, playID, descriptor, capabilities, allowTranscode, input.StartTimeTicks))
			}
			binding, _, native, openErr := handler.playSessions.openAndTouch(request.Context(), session, item.ID, playID, input.MediaSourceId, input.StartTimeTicks)
			if openErr != nil {
				handler.writePlaybackError(response, request, openErr)
				return
			}
			applyResolvedMediaSource(result.MediaSources, binding.MediaSourceID, native)
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
			result := PlaybackInfoResponse{PlaySessionId: playID, MediaSources: make([]MediaSourceInfo, 0, len(descriptors))}
			for _, descriptor := range descriptors {
				result.MediaSources = append(result.MediaSources, mediaSourceDTO(item, playID, descriptor, capabilities, allowTranscode, input.StartTimeTicks))
			}
			binding, _, native, openErr := handler.playSessions.openAndTouch(request.Context(), session, item.ID, playID, input.MediaSourceId, input.StartTimeTicks)
			if openErr != nil {
				handler.writePlaybackError(response, request, openErr)
				return
			}
			applyResolvedMediaSource(result.MediaSources, binding.MediaSourceID, native)
			handler.writeJSON(response, http.StatusOK, result)
			return
		}
		if input.MediaSourceId != item.ID || handler.playSessions.candidateExists(session, item.ID, input.MediaSourceId) {
			handler.writeCompatError(response, http.StatusNotFound, "PlaybackSessionNotFound", "The playback session or source is invalid or expired")
			return
		}
	}
	result, err := handler.registerPlaybackInfo(request.Context(), session, item, capabilities, allowTranscode, options, input.StartTimeTicks)
	if err != nil {
		handler.writePlaybackError(response, request, err)
		return
	}
	playID := result.PlaySessionId
	if input.MediaSourceId != "" {
		binding, _, native, openErr := handler.playSessions.openAndTouch(request.Context(), session, item.ID, playID, input.MediaSourceId, input.StartTimeTicks)
		if openErr != nil {
			_ = handler.playSessions.close(context.WithoutCancel(request.Context()), session, item.ID, playID, input.MediaSourceId)
			handler.writePlaybackError(response, request, openErr)
			return
		}
		applyResolvedMediaSource(result.MediaSources, binding.MediaSourceID, native)
	}
	handler.writeJSON(response, http.StatusOK, result)
}

func (handler *Handler) detailMediaSources(ctx context.Context, session AuthenticatedSession, item watchstate.CatalogTitle) []MediaSourceInfo {
	if handler == nil || handler.playback == nil || handler.playSessions == nil || item.ID == "" || item.MediaType != "movie" && item.MediaType != "episode" {
		return nil
	}
	input := PlaybackInfoRequest{DeviceProfile: conservativeCompatibilityProfile()}
	capabilities, allowTranscode, err := playbackCapabilities(input)
	if err != nil {
		return nil
	}
	options, releaseOptions, err := handler.playbackOptions(ctx, session, item, capabilities, allowTranscode)
	if err != nil {
		return nil
	}
	defer releaseOptions()
	result, err := handler.registerPlaybackInfo(ctx, session, item, capabilities, allowTranscode, options, 0)
	if err != nil {
		return nil
	}
	return result.MediaSources
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

func (handler *Handler) registerPlaybackInfo(ctx context.Context, session AuthenticatedSession, item watchstate.CatalogTitle, capabilities playback.Capabilities, allowTranscode bool, options []playback.SourceOption, startTimeTicks int64) (PlaybackInfoResponse, error) {
	playID, descriptors, err := handler.playSessions.register(ctx, session, item.ID, capabilities, allowTranscode, options)
	if err != nil {
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
	if err := validateQueryBudget(request.URL.Query()); err != nil {
		handler.writeStreamError(response, request, http.StatusBadRequest, "InvalidRequest", "The stream request is invalid")
		return
	}
	playID, mediaID, startTimeTicks, err := parseStreamSelectors(request.URL.Query())
	if err != nil {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The playback session is invalid or expired")
		return
	}
	itemID := request.PathValue("id")
	session, ok := handler.authenticateStreamRequest(response, request, itemID, playID, mediaID)
	if !ok {
		return
	}
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, itemID)
	if err != nil {
		handler.writeStreamPlaybackError(response, request, err)
		return
	}
	binding, handle, _, err := handler.playSessions.openAndTouch(request.Context(), session, item.ID, playID, mediaID, startTimeTicks)
	if err != nil {
		handler.writeStreamPlaybackError(response, request, err)
		return
	}
	if binding.ItemID != item.ID || binding.MediaSourceID != mediaID {
		handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The playback session is invalid or expired")
		return
	}
	deliveryRequest := scopedStreamDeliveryRequest(request, playID)
	if err := handler.playback.Serve(response, deliveryRequest, handle); err != nil {
		_ = handler.playSessions.close(context.WithoutCancel(request.Context()), session, item.ID, playID, mediaID)
		handler.writeStreamPlaybackError(response, request, err)
	}
}

func (handler *Handler) authenticateStreamRequest(response http.ResponseWriter, request *http.Request, itemID, playID, mediaID string) (AuthenticatedSession, bool) {
	apiKey, found, err := queryScalar(request.URL.Query(), "api_key")
	if err != nil {
		handler.writeStreamError(response, request, http.StatusUnauthorized, "Unauthorized", "A valid stream capability is required")
		return AuthenticatedSession{}, false
	}
	playCapability := found && subtle.ConstantTimeCompare([]byte(apiKey), []byte(playID)) == 1
	if playCapability && !compatCredentialHeaderPresent(request) {
		cached, valid := handler.playSessions.streamSession(playID, itemID, mediaID)
		if !valid {
			handler.writeStreamError(response, request, http.StatusNotFound, "PlaybackSessionNotFound", "The playback session is invalid or expired")
			return AuthenticatedSession{}, false
		}
		fresh, revalidateErr := handler.authentication.Revalidate(request.Context(), cached)
		ownerMismatch := revalidateErr == nil && !sameAuthenticatedSessionOwner(cached, fresh)
		if revalidateErr != nil || ownerMismatch {
			if errors.Is(revalidateErr, ErrInvalidCompatCredential) || ownerMismatch {
				_ = handler.playSessions.close(context.WithoutCancel(request.Context()), cached, itemID, playID, mediaID)
				handler.writeStreamError(response, request, http.StatusUnauthorized, "Unauthorized", "The stream capability is no longer valid")
			} else {
				handler.writeStreamError(response, request, http.StatusInternalServerError, "InternalError", "The stream authorization could not be verified")
			}
			return AuthenticatedSession{}, false
		}
		return fresh, true
	}
	authRequest := request
	if playCapability {
		authRequest = request.Clone(request.Context())
		clonedURL := *request.URL
		values := clonedURL.Query()
		values.Del("api_key")
		clonedURL.RawQuery = values.Encode()
		authRequest.URL = &clonedURL
	}
	return handler.authenticateRequest(response, authRequest, !playCapability)
}

func scopedStreamDeliveryRequest(request *http.Request, playID string) *http.Request {
	cloned := request.Clone(request.Context())
	clonedURL := *request.URL
	values := clonedURL.Query()
	values.Set("api_key", playID)
	clonedURL.RawQuery = values.Encode()
	cloned.URL = &clonedURL
	return cloned
}

func compatCredentialHeaderPresent(request *http.Request) bool {
	if request == nil {
		return false
	}
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		if len(request.Header.Values(name)) != 0 {
			return true
		}
	}
	parameters, found, err := collectAuthorizationParameters(request.Header)
	if err != nil {
		return true
	}
	if found {
		_, hasToken := parameters["token"]
		return hasToken
	}
	return len(request.Header.Values("Authorization")) != 0
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
	if len(input.UserId) > 128 || len(input.MediaSourceId) > 128 || input.StartTimeTicks < 0 || input.StartTimeTicks > 7*24*60*60*TicksPerSecond {
		return PlaybackInfoRequest{}, ErrInvalidQuery
	}
	return input, nil
}
func emptyDeviceProfile(profile DeviceProfile) bool {
	return len(profile.DirectPlayProfiles) == 0 && len(profile.TranscodingProfiles) == 0 && len(profile.SubtitleProfiles) == 0
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
	if err == nil || !isVidHubClient(session.Client) || !validDeviceProfileBounds(input.DeviceProfile) {
		return capabilities, allowTranscode, err
	}
	fallback := conservativeCompatibilityProfile()
	fallback.MaxStreamingBitrate = input.DeviceProfile.MaxStreamingBitrate
	input.DeviceProfile = fallback
	return playbackCapabilities(input)
}

func validDeviceProfileBounds(profile DeviceProfile) bool {
	if len(profile.Name) > 128 || len(profile.DirectPlayProfiles) > 32 || len(profile.TranscodingProfiles) > 32 || len(profile.SubtitleProfiles) > 32 ||
		profile.MaxStreamingBitrate != 0 && (profile.MaxStreamingBitrate < 64_000 || profile.MaxStreamingBitrate > 200_000_000) {
		return false
	}
	boundedList := func(value string) bool { return len(value) <= 2048 }
	boundedScalar := func(value string) bool { return len(value) <= 64 }
	for _, direct := range profile.DirectPlayProfiles {
		if !boundedList(direct.Container) || !boundedList(direct.AudioCodec) || !boundedList(direct.VideoCodec) || !boundedScalar(direct.Type) {
			return false
		}
	}
	for _, transcode := range profile.TranscodingProfiles {
		if !boundedList(transcode.Container) || !boundedList(transcode.AudioCodec) || !boundedList(transcode.VideoCodec) ||
			!boundedScalar(transcode.Type) || !boundedScalar(transcode.Protocol) || !boundedScalar(transcode.Context) {
			return false
		}
	}
	for _, subtitle := range profile.SubtitleProfiles {
		if !boundedList(subtitle.Format) || !boundedScalar(subtitle.Method) {
			return false
		}
	}
	return true
}

func playbackCapabilities(input PlaybackInfoRequest) (playback.Capabilities, bool, error) {
	profile := input.DeviceProfile
	if !validDeviceProfileBounds(profile) {
		return playback.Capabilities{}, false, ErrInvalidQuery
	}
	capabilities := playback.Capabilities{}
	preferDirect := true
	capabilities.PreferDirectPlay = &preferDirect
	profiles := make(map[string]struct{})
	addProfile := func(container, video, audio string) error {
		container, video, audio = normalizeCapability(container), normalizeCapability(video), normalizeCapability(audio)
		if container == "" || video == "" {
			return nil
		}
		key := container + "\x00" + video + "\x00" + audio
		if _, exists := profiles[key]; exists {
			return nil
		}
		if len(profiles) >= 32 {
			return ErrInvalidQuery
		}
		profiles[key] = struct{}{}
		capabilities.MediaProfiles = append(capabilities.MediaProfiles, playback.MediaProfile{Container: container, VideoCodec: video, AudioCodec: audio})
		return nil
	}
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
		if len(audios) == 0 {
			audios = []string{""}
		}
		for _, container := range containers {
			if container == "m3u8" || container == "hls" {
				appendUnique(&capabilities.StreamingProtocols, "hls")
			} else {
				appendUnique(&capabilities.StreamingProtocols, "http")
			}
			appendUnique(&capabilities.Containers, normalizedContainer(container))
			for _, video := range videos {
				appendUnique(&capabilities.VideoCodecs, video)
				for _, audio := range audios {
					if audio != "" {
						appendUnique(&capabilities.AudioCodecs, audio)
					}
					if err := addProfile(normalizedContainer(container), video, audio); err != nil {
						return playback.Capabilities{}, false, err
					}
				}
			}
		}
	}
	allowTranscode := false
	for _, transcode := range profile.TranscodingProfiles {
		if transcode.Type != "" && !strings.EqualFold(transcode.Type, "Video") || transcode.Context != "" && !strings.EqualFold(transcode.Context, "Streaming") {
			continue
		}
		protocol := normalizeCapability(transcode.Protocol)
		if protocol != "hls" {
			continue
		}
		containers, err := capabilityList(transcode.Container)
		if err != nil {
			return playback.Capabilities{}, false, err
		}
		supportedContainer := false
		for _, container := range containers {
			switch normalizedContainer(container) {
			case "mp4", "hls", "ts", "mpegts":
				supportedContainer = true
			}
		}
		if !supportedContainer {
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
		appendUnique(&capabilities.StreamingProtocols, "hls")
		for _, video := range videos {
			for _, audio := range audios {
				if err := addProfile("mp4", video, audio); err != nil {
					return playback.Capabilities{}, false, err
				}
				if video == "h264" && audio == "aac" {
					appendUnique(&capabilities.ProcessingModes, "transcode")
					appendUnique(&capabilities.ProcessingModes, "transcode_audio")
				}
				appendUnique(&capabilities.ProcessingModes, "remux")
				allowTranscode = true
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

func usableSourceOptions(options []playback.SourceOption, capabilities playback.Capabilities, allowTranscode bool) []playback.SourceOption {
	result := make([]playback.SourceOption, 0, len(options))
	for _, option := range options {
		protocol := normalizeCapability(option.Protocol)
		if protocol == "external" || protocol == "youtube" || option.SourceRef == "" {
			continue
		}
		direct := containsFold(capabilities.StreamingProtocols, protocol) &&
			(protocol == "hls" || option.Container == "" || containsFold(capabilities.Containers, normalizedContainer(option.Container)))
		if !direct && !allowTranscode || protocol != "http" && protocol != "hls" {
			continue
		}
		result = append(result, option)
	}
	return result
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
	values := url.Values{"PlaySessionId": {playID}, "MediaSourceId": {descriptor.ID}, "Static": {"true"}, "api_key": {playID}}
	if startTimeTicks > 0 {
		values.Set("StartTimeTicks", strconv.FormatInt(startTimeTicks, 10))
	}
	container := normalizedContainer(descriptor.Container)
	pathContainer := container
	protocol := normalizeCapability(descriptor.Protocol)
	if protocol == "hls" || pathContainer == "hls" {
		pathContainer = "m3u8"
	}
	streamPath := compatibilityStreamPath(item.ID, pathContainer, values)
	direct := containsFold(capabilities.StreamingProtocols, protocol) &&
		(protocol == "hls" || container == "" || containsFold(capabilities.Containers, container))
	source := MediaSourceInfo{
		Id: descriptor.ID, Name: descriptor.Name, Path: streamPath, Container: container,
		Protocol: "Http", Type: "Default", IsRemote: false, SupportsDirectPlay: direct,
		SupportsDirectStream: direct, SupportsTranscoding: allowTranscode, MediaStreams: []MediaStreamInfo{},
		RunTimeTicks: catalogRuntimeTicks(item),
	}
	if allowTranscode {
		source.TranscodingUrl = compatibilityStreamPath(item.ID, "m3u8", values)
		source.TranscodingSubProtocol = "Hls"
		source.TranscodingContainer = "hls"
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

func catalogRuntimeTicks(item watchstate.CatalogTitle) *int64 {
	if item.RuntimeMinutes == nil || *item.RuntimeMinutes <= 0 {
		return nil
	}
	value := MinutesToTicks(int64(*item.RuntimeMinutes))
	return &value
}

func applyResolvedMediaSource(sources []MediaSourceInfo, mediaID string, session playback.Session) {
	for index := range sources {
		if sources[index].Id != mediaID {
			continue
		}
		for _, native := range session.Sources {
			if native.ID != session.SelectedSourceID {
				continue
			}
			sources[index].Container = normalizedContainer(native.Container)
			sources[index].SupportsDirectPlay = native.Mode == "direct"
			sources[index].SupportsDirectStream = native.Mode == "direct" || native.Mode == "remux"
			sources[index].SupportsTranscoding = native.Mode == "transcode" || native.Mode == "transcode_audio" || native.Mode == "remux"
			if native.Media != nil {
				sources[index].Name = verifiedCompatibilitySourceName(sources[index].Name, native.Media, session.SelectedAudioTrack)
				sources[index].MediaStreams, sources[index].DefaultAudioStreamIndex = compatibilityMediaStreams(native.Media, session.SelectedAudioTrack)
				if native.Media.DurationSeconds > 0 {
					ticks := SecondsToTicks(int64(native.Media.DurationSeconds))
					sources[index].RunTimeTicks = &ticks
				}
				var kbps int64
				for _, track := range append(append([]playback.MediaTrack(nil), native.Media.VideoTracks...), native.Media.AudioTracks...) {
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
	streams := make([]MediaStreamInfo, 0, min(64, len(media.VideoTracks)+len(media.AudioTracks)+len(media.SubtitleTracks)))
	appendTracks := func(tracks []playback.MediaTrack, mediaType string) {
		for _, track := range tracks {
			if len(streams) >= 64 || track.Index < 0 {
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
	if value < 64_000 || value > 200_000_000 {
		return 0, ErrInvalidQuery
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
