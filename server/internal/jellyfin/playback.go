package jellyfin

import (
	"context"
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

const maximumPlaybackInfoBodyBytes = 256 << 10

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
	if request.Method == http.MethodGet && emptyDeviceProfile(input.DeviceProfile) {
		input.DeviceProfile = conservativeCompatibilityProfile()
	}
	capabilities, allowTranscode, err := playbackCapabilities(input)
	if err != nil {
		handler.writeCompatError(response, http.StatusUnprocessableEntity, "PlaybackProfileUnsupported", "The device playback profile is unsupported")
		return
	}
	resourceID := strings.TrimSpace(item.ResourceID)
	if resourceID == "" {
		resourceID = preferredPlaybackResource(item.ProviderIDs)
	}
	if resourceID == "" {
		handler.writeCompatError(response, http.StatusNotFound, "NoPlayableSource", "No playable source was found")
		return
	}
	if input.MediaSourceId != "" {
		playID, descriptors, reused := handler.playSessions.reuseCandidate(session, item.ID, input.MediaSourceId, capabilities, allowTranscode, nil)
		if reused {
			result := PlaybackInfoResponse{PlaySessionId: playID, MediaSources: make([]MediaSourceInfo, 0, len(descriptors))}
			for _, descriptor := range descriptors {
				result.MediaSources = append(result.MediaSources, mediaSourceDTO(item.ID, playID, descriptor, capabilities, allowTranscode, input.StartTimeTicks))
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
	sourcesInput := playback.SourcesInput{
		MediaType: item.MediaType, AddonID: item.SourceAddonID, ResourceID: resourceID, Capabilities: capabilities,
	}
	var list playback.SourceList
	reserved := false
	if reserver, ok := handler.playback.(sourceReferenceReserver); ok {
		list, err = reserver.SourcesAndPin(request.Context(), session.Principal, sourcesInput)
		reserved = err == nil
	} else {
		list, err = handler.playback.Sources(request.Context(), session.Principal, sourcesInput)
	}
	if err != nil {
		handler.writePlaybackError(response, request, err)
		return
	}
	if reserved {
		references := sourceOptionReferenceIDs(list.Sources)
		defer handler.playback.(sourceReferenceReserver).UnpinSourceReferences(session.Principal, references)
	}
	options := usableSourceOptions(list.Sources, capabilities, allowTranscode)
	if len(options) == 0 {
		handler.writeCompatError(response, http.StatusNotFound, "NoPlayableSource", "No playable source was found")
		return
	}
	if input.MediaSourceId != "" {
		playID, descriptors, reused := handler.playSessions.reuseCandidate(session, item.ID, input.MediaSourceId, capabilities, allowTranscode, options)
		if reused {
			result := PlaybackInfoResponse{PlaySessionId: playID, MediaSources: make([]MediaSourceInfo, 0, len(descriptors))}
			for _, descriptor := range descriptors {
				result.MediaSources = append(result.MediaSources, mediaSourceDTO(item.ID, playID, descriptor, capabilities, allowTranscode, input.StartTimeTicks))
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
	playID, descriptors, err := handler.playSessions.register(request.Context(), session, item.ID, capabilities, allowTranscode, options)
	if err != nil {
		handler.writePlaybackError(response, request, err)
		return
	}
	result := PlaybackInfoResponse{PlaySessionId: playID, MediaSources: make([]MediaSourceInfo, 0, len(descriptors))}
	for _, descriptor := range descriptors {
		result.MediaSources = append(result.MediaSources, mediaSourceDTO(item.ID, playID, descriptor, capabilities, allowTranscode, input.StartTimeTicks))
	}
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

func (handler *Handler) handleStream(response http.ResponseWriter, request *http.Request) {
	if handler.playback == nil || handler.catalog == nil || handler.playSessions == nil {
		http.NotFound(response, request)
		return
	}
	session, ok := handler.authenticateRequest(response, request, true)
	if !ok {
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
	item, err := handler.catalog.GetCatalogTitle(request.Context(), session.Principal, request.PathValue("id"))
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
	if err := handler.playback.Serve(response, request, handle); err != nil {
		_ = handler.playSessions.close(context.WithoutCancel(request.Context()), session, item.ID, playID, mediaID)
		handler.writeStreamPlaybackError(response, request, err)
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
func validDeviceProfileBounds(profile DeviceProfile) bool {
	if len(profile.Name) > 128 || len(profile.DirectPlayProfiles) > 32 || len(profile.TranscodingProfiles) > 32 || len(profile.SubtitleProfiles) > 32 {
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

func mediaSourceDTO(itemID, playID string, descriptor playSourceDescriptor, capabilities playback.Capabilities, allowTranscode bool, startTimeTicks int64) MediaSourceInfo {
	values := url.Values{"PlaySessionId": {playID}, "MediaSourceId": {descriptor.ID}}
	if startTimeTicks > 0 {
		values.Set("StartTimeTicks", strconv.FormatInt(startTimeTicks, 10))
	}
	streamPath := "/Videos/" + url.PathEscape(itemID) + "/stream?" + values.Encode()
	protocol := normalizeCapability(descriptor.Protocol)
	direct := containsFold(capabilities.StreamingProtocols, protocol) &&
		(protocol == "hls" || descriptor.Container == "" || containsFold(capabilities.Containers, normalizedContainer(descriptor.Container)))
	source := MediaSourceInfo{
		Id: descriptor.ID, Name: descriptor.Name, Path: streamPath, Container: normalizedContainer(descriptor.Container),
		Protocol: "Http", Type: "Default", IsRemote: false, SupportsDirectPlay: direct,
		SupportsDirectStream: direct || allowTranscode, SupportsTranscoding: allowTranscode,
	}
	if allowTranscode {
		source.TranscodingUrl = streamPath
		source.TranscodingSubProtocol = "Hls"
		source.TranscodingContainer = "hls"
	}
	return source
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
			sources[index].SupportsTranscoding = native.Mode == "transcode" || native.Mode == "transcode_audio"
			if native.Media != nil {
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
