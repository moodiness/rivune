package playback

import (
	"context"
	"sort"
	"strings"
)

const (
	processingRemux          = "remux"
	processingTranscodeAudio = "transcode_audio"
	processingTranscode      = "transcode"
)

const (
	decisionDirectSupported        = "direct_supported"
	decisionRemuxRequired          = "remux_required"
	decisionAudioTranscodeRequired = "audio_transcode_required"
	decisionVideoTranscodeRequired = "video_transcode_required"
	decisionSubtitleBurnRequired   = "subtitle_burn_required"
)

type sourceCandidate struct {
	sourceIndex    int
	assetIndex     int
	directHint     bool
	resolutionHint int
}

func sourceResolutionHint(source Source) int {
	value := strings.ToLower(source.Hint)
	switch {
	case strings.Contains(value, "2160p") || strings.Contains(value, "4k"):
		return 2160
	case strings.Contains(value, "1080p"):
		return 1080
	case strings.Contains(value, "720p"):
		return 720
	case strings.Contains(value, "480p"):
		return 480
	default:
		return 0
	}
}

func (service *Service) decidePlaybackSource(ctx context.Context, sources []Source, assets []storedAsset, capabilities Capabilities, policy ...bool) error {
	allowTranscoding := len(policy) == 0 || policy[0]
	if service.processor == nil {
		for index := range sources {
			if sources[index].Compatible {
				sources[index].Decision = directDecision(MediaInspection{})
				if assetIndex := storedAssetIndex(assets, sources[index].ID); assetIndex >= 0 {
					assets[assetIndex].Decision = clonePlaybackDecision(sources[index].Decision)
				}
				return nil
			}
		}
		return nil
	}

	candidates := make([]sourceCandidate, 0, len(sources))
	for sourceIndex := range sources {
		source := &sources[sourceIndex]
		if source.Mode == "youtube" || source.Mode == "external" {
			if source.Compatible {
				return nil
			}
			continue
		}
		if source.Mode != "direct" || source.URL == "" {
			continue
		}
		assetIndex := storedAssetIndex(assets, source.ID)
		if assetIndex < 0 {
			continue
		}
		candidates = append(candidates, sourceCandidate{
			sourceIndex: sourceIndex, assetIndex: assetIndex, directHint: source.Compatible,
			resolutionHint: sourceResolutionHint(*source),
		})
		source.Compatible = false
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].resolutionHint != candidates[right].resolutionHint {
			return candidates[left].resolutionHint > candidates[right].resolutionHint
		}
		if candidates[left].directHint != candidates[right].directHint {
			return candidates[left].directHint
		}
		return remuxHintRank(sources[candidates[left].sourceIndex].Hint) < remuxHintRank(sources[candidates[right].sourceIndex].Hint)
	})

	conversionDenied := false
	capabilityMissing := false
	fallbackHeight := 0
	var fallbackCandidate sourceCandidate
	var fallbackInspection MediaInspection
	var fallbackMode string
	var fallbackDecision *PlaybackDecision
	for _, candidate := range candidates {
		inspection, err := service.probeMedia(ctx, assets[candidate.assetIndex])
		if err != nil {
			continue
		}
		source := &sources[candidate.sourceIndex]
		source.Media = &inspection
		if inspection.Container != "" {
			source.Container = inspection.Container
		}
		mode, decision := playbackMode(*source, inspection, capabilities)
		video := primaryTrack(inspection.VideoTracks)
		if mode != "" && candidate.resolutionHint > 0 && video != nil && video.Height < candidate.resolutionHint*9/10 {
			if mode == processingTranscodeAudio || mode == processingTranscode {
				if !allowTranscoding {
					conversionDenied = true
					continue
				}
			}
			if video.Height > fallbackHeight {
				fallbackHeight = video.Height
				fallbackCandidate = candidate
				fallbackInspection = inspection
				fallbackMode = mode
				fallbackDecision = decision
			}
			continue
		}
		switch mode {
		case "direct", processingRemux:
			applyPlaybackDecision(sources, assets, candidate, inspection, mode, decision, capabilities)
			return nil
		case processingTranscodeAudio, processingTranscode:
			if !allowTranscoding {
				conversionDenied = true
				continue
			}
			applyPlaybackDecision(sources, assets, candidate, inspection, mode, decision, capabilities)
			return nil
		default:
			if !allowTranscoding && transcodingRequired(inspection, capabilities) {
				conversionDenied = true
			} else {
				capabilityMissing = true
			}
		}
	}
	if fallbackHeight > 0 {
		applyPlaybackDecision(sources, assets, fallbackCandidate, fallbackInspection, fallbackMode, fallbackDecision, capabilities)
		return nil
	}
	if conversionDenied {
		return ErrTranscodingDisabled
	}
	if capabilityMissing {
		return ErrClientCapabilityMissing
	}
	return nil
}

func directDecision(inspection MediaInspection) *PlaybackDecision {
	return &PlaybackDecision{
		Reason: decisionDirectSupported, VideoAction: "copy", AudioAction: "copy", SubtitleAction: "none",
		Source: decisionSource(inspection),
	}
}

func decisionSource(inspection MediaInspection) *PlaybackDecisionSource {
	video := primaryTrack(inspection.VideoTracks)
	audio := primaryTrack(inspection.AudioTracks)
	if video == nil && audio == nil && inspection.Container == "" {
		return nil
	}
	source := &PlaybackDecisionSource{Container: inspection.Container, HDRFormat: inspection.HDRFormat}
	if video != nil {
		source.VideoCodec = normalizedCodec(video.Codec)
		source.Height = video.Height
		source.VideoBitrateKbps = video.BitrateKbps
	}
	if audio != nil {
		source.AudioCodec = normalizedCodec(audio.Codec)
	}
	return source
}

func applyPlaybackDecision(sources []Source, assets []storedAsset, candidate sourceCandidate, inspection MediaInspection, mode string, decision *PlaybackDecision, capabilities Capabilities) {
	source := &sources[candidate.sourceIndex]
	source.Media = &inspection
	source.Mode = mode
	source.Compatible = true
	source.Decision = clonePlaybackDecision(decision)
	asset := &assets[candidate.assetIndex]
	asset.Decision = clonePlaybackDecision(decision)
	if mode == "direct" {
		return
	}
	protocol, container := processingOutput()
	source.Protocol = protocol
	source.Container = container
	asset.Kind = mode
	if decision != nil {
		asset.ToneMap = decision.ToneMapping
		if decision.Target != nil {
			asset.TargetHeight = decision.Target.Height
			asset.VideoBitrateKbps = decision.Target.VideoBitrateKbps
		}
	}
	asset.MaximumAudioChannels = capabilities.MaximumAudioChannels
}

func playbackMode(source Source, inspection MediaInspection, capabilities Capabilities) (string, *PlaybackDecision) {
	video := primaryTrack(inspection.VideoTracks)
	if video == nil {
		return "", nil
	}
	audio := primaryTrack(inspection.AudioTracks)
	if directPlaybackSupported(source, inspection, video, audio, capabilities) {
		return "direct", directDecision(inspection)
	}
	if remuxSupported(inspection, capabilities) {
		return processingRemux, processingDecision(decisionRemuxRequired, "copy", "copy", inspection, capabilities, false)
	}
	if audioTranscodeSupported(inspection, capabilities) {
		return processingTranscodeAudio, processingDecision(decisionAudioTranscodeRequired, "copy", "transcode", inspection, capabilities, false)
	}
	if fullTranscodeSupported(capabilities) {
		toneMap := !clientSupportsHDR(inspection.HDRFormat, capabilities)
		return processingTranscode, processingDecision(decisionVideoTranscodeRequired, "transcode", "transcode", inspection, capabilities, toneMap)
	}
	return "", nil
}

func directPlaybackSupported(source Source, inspection MediaInspection, video, audio *MediaTrack, capabilities Capabilities) bool {
	container := source.Container
	if inspection.Container != "" {
		container = inspection.Container
	}
	return supports(capabilities.StreamingProtocols, source.Protocol) &&
		mediaProfileSupported(container, video, audio, capabilities) &&
		videoWithinClientLimits(video, inspection.HDRFormat, capabilities) && audioWithinClientLimits(audio, capabilities)
}

func transcodingRequired(inspection MediaInspection, capabilities Capabilities) bool {
	copyCapabilities := cloneCapabilities(capabilities)
	if !requestedProcessingMode(copyCapabilities.ProcessingModes, processingRemux) {
		copyCapabilities.ProcessingModes = append(copyCapabilities.ProcessingModes, processingRemux)
	}
	return !remuxSupported(inspection, copyCapabilities)
}

func remuxSupported(inspection MediaInspection, capabilities Capabilities) bool {
	if !requestedProcessingMode(capabilities.ProcessingModes, processingRemux) || !processingOutputSupported(capabilities) {
		return false
	}
	video := primaryTrack(inspection.VideoTracks)
	if video == nil || !mp4RemuxableVideo(video.Codec) || !videoWithinClientLimits(video, inspection.HDRFormat, capabilities) {
		return false
	}
	if len(inspection.AudioTracks) == 0 {
		return mediaProfileSupported("mp4", video, nil, capabilities)
	}
	return compatibleRemuxAudioTrack(*video, inspection.AudioTracks, capabilities) != nil
}

func audioTranscodeSupported(inspection MediaInspection, capabilities Capabilities) bool {
	if !requestedProcessingMode(capabilities.ProcessingModes, processingTranscodeAudio) || !processingOutputSupported(capabilities) {
		return false
	}
	video := primaryTrack(inspection.VideoTracks)
	if video == nil || !mp4RemuxableVideo(video.Codec) || !videoWithinClientLimits(video, inspection.HDRFormat, capabilities) {
		return false
	}
	targetAudio := &MediaTrack{Codec: "aac", Channels: targetAudioChannels(capabilities)}
	return mediaProfileSupported("mp4", video, targetAudio, capabilities)
}

func fullTranscodeSupported(capabilities Capabilities) bool {
	if !requestedProcessingMode(capabilities.ProcessingModes, processingTranscode) || !processingOutputSupported(capabilities) {
		return false
	}
	video := &MediaTrack{Codec: "h264"}
	audio := &MediaTrack{Codec: "aac", Channels: targetAudioChannels(capabilities)}
	return mediaProfileSupported("mp4", video, audio, capabilities)
}

func processingDecision(reason, videoAction, audioAction string, inspection MediaInspection, capabilities Capabilities, toneMap bool) *PlaybackDecision {
	protocol, container := processingOutput()
	target := &PlaybackDecisionTarget{Protocol: protocol, Container: container}
	video := primaryTrack(inspection.VideoTracks)
	audio := primaryTrack(inspection.AudioTracks)
	if videoAction == "copy" && video != nil {
		target.VideoCodec = normalizedCodec(video.Codec)
		target.Height = video.Height
		target.VideoBitrateKbps = video.BitrateKbps
	} else {
		target.VideoCodec = "h264"
		if video != nil {
			target.Height = video.Height
		}
		if capabilities.MaximumHeight > 0 && (target.Height == 0 || target.Height > capabilities.MaximumHeight) {
			target.Height = capabilities.MaximumHeight
		}
		target.VideoBitrateKbps = transcodeVideoBitrateKbps(capabilities)
	}
	if audioAction == "copy" && audio != nil {
		target.AudioCodec = normalizedCodec(audio.Codec)
	} else if audio != nil {
		target.AudioCodec = "aac"
	}
	return &PlaybackDecision{
		Reason: reason, VideoAction: videoAction, AudioAction: audioAction,
		SubtitleAction: "none", ToneMapping: toneMap, Source: decisionSource(inspection), Target: target,
	}
}
func transcodeVideoBitrateKbps(capabilities Capabilities) int {
	serverMaximum := capabilities.TranscodeVideoBitrateKbps
	clientMaximum := capabilities.MaximumVideoBitrateKbps
	if clientMaximum > 0 && (serverMaximum <= 0 || clientMaximum < serverMaximum) {
		return clientMaximum
	}
	return serverMaximum
}

func processingOutputSupported(capabilities Capabilities) bool {
	return supports(capabilities.StreamingProtocols, "hls")
}

func processingOutput() (string, string) {
	return "hls", "hls"
}

func videoWithinClientLimits(video *MediaTrack, hdrFormat string, capabilities Capabilities) bool {
	if video == nil || !supportsCodec(capabilities.VideoCodecs, video.Codec) {
		return false
	}
	if capabilities.MaximumHeight > 0 && (video.Height <= 0 || video.Height > capabilities.MaximumHeight) {
		return false
	}
	if capabilities.MaximumVideoBitrateKbps > 0 &&
		(video.BitrateKbps <= 0 || video.BitrateKbps > capabilities.MaximumVideoBitrateKbps) {
		return false
	}
	return clientSupportsHDR(hdrFormat, capabilities)
}

func clientSupportsHDR(format string, capabilities Capabilities) bool {
	return format == "" || len(capabilities.HDRFormats) > 0 && supports(capabilities.HDRFormats, format)
}

func audioWithinClientLimits(audio *MediaTrack, capabilities Capabilities) bool {
	if audio == nil {
		return true
	}
	if !supportsCodec(capabilities.AudioCodecs, audio.Codec) {
		return false
	}
	return capabilities.MaximumAudioChannels == 0 ||
		audio.Channels > 0 && audio.Channels <= capabilities.MaximumAudioChannels
}

func targetAudioChannels(capabilities Capabilities) int {
	if capabilities.MaximumAudioChannels > 0 && capabilities.MaximumAudioChannels < 2 {
		return capabilities.MaximumAudioChannels
	}
	return 2
}

func requestedProcessingMode(values []string, mode string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), mode) {
			return true
		}
	}
	return false
}

func directMediaSupported(inspection MediaInspection, capabilities Capabilities) bool {
	video := primaryTrack(inspection.VideoTracks)
	return video != nil && mediaProfileSupported(inspection.Container, video, primaryTrack(inspection.AudioTracks), capabilities) &&
		videoWithinClientLimits(video, inspection.HDRFormat, capabilities) && audioWithinClientLimits(primaryTrack(inspection.AudioTracks), capabilities)
}

func mediaProfileSupported(container string, video, audio *MediaTrack, capabilities Capabilities) bool {
	container = strings.ToLower(strings.TrimSpace(container))
	if container == "" {
		return false
	}
	if len(capabilities.MediaProfiles) == 0 {
		return supportsContainer(capabilities.Containers, container) &&
			video != nil && supportsCodec(capabilities.VideoCodecs, video.Codec) &&
			(audio == nil || supportsCodec(capabilities.AudioCodecs, audio.Codec))
	}
	for _, profile := range capabilities.MediaProfiles {
		if container != "" && !mediaProfileContainerMatches(profile.Container, container) {
			continue
		}
		if video == nil || normalizedCodec(profile.VideoCodec) != normalizedCodec(video.Codec) {
			continue
		}
		if audio == nil || normalizedCodec(profile.AudioCodec) == normalizedCodec(audio.Codec) {
			return true
		}
	}
	return false
}

func mediaProfileContainerMatches(profile, candidate string) bool {
	profile = strings.ToLower(strings.TrimSpace(profile))
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if candidate == "m4v" || candidate == "mov" {
		candidate = "mp4"
	}
	return profile == candidate
}

func compatibleRemuxAudioTrack(video MediaTrack, tracks []MediaTrack, capabilities Capabilities) *MediaTrack {
	for index := range tracks {
		if mp4RemuxableAudio(tracks[index].Codec) && audioWithinClientLimits(&tracks[index], capabilities) &&
			mediaProfileSupported("mp4", &video, &tracks[index], capabilities) {
			return &tracks[index]
		}
	}
	return nil
}

func primaryTrack(tracks []MediaTrack) *MediaTrack {
	if len(tracks) == 0 {
		return nil
	}
	return &tracks[0]
}

func supportsCodec(values []string, codec string) bool {
	codec = normalizedCodec(codec)
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if normalizedCodec(value) == codec {
			return true
		}
	}
	return false
}

func normalizedCodec(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "avc", "avc1", "h.264", "x264":
		return "h264"
	case "hevc", "hev1", "hvc1", "h.265", "x265":
		return "h265"
	case "av01":
		return "av1"
	case "vp09":
		return "vp9"
	case "mp4a", "mp4a.40.2":
		return "aac"
	case "e-ac-3", "eac-3":
		return "eac3"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func mp4RemuxableVideo(codec string) bool {
	switch normalizedCodec(codec) {
	case "h264", "h265", "av1":
		return true
	default:
		return false
	}
}

func mp4RemuxableAudio(codec string) bool {
	switch normalizedCodec(codec) {
	case "aac", "ac3", "eac3", "mp3", "alac", "opus":
		return true
	default:
		return false
	}
}

func storedAssetIndex(assets []storedAsset, id string) int {
	for index := range assets {
		if assets[index].ID == id {
			return index
		}
	}
	return -1
}

func remuxHintRank(rawURL string) int {
	value := strings.ToLower(rawURL)
	rank := 2
	if strings.Contains(value, "h264") || strings.Contains(value, "x264") || strings.Contains(value, "avc") {
		rank--
	}
	if strings.Contains(value, "aac") {
		rank--
	}
	return rank
}
