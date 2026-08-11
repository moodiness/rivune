package playback

import (
	"context"
	"sort"
	"strconv"
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

const (
	reasonContainerNotSupported  = "container_not_supported"
	reasonVideoCodecNotSupported = "video_codec_not_supported"
	reasonAudioCodecNotSupported = "audio_codec_not_supported"
	reasonResolutionLimit        = "resolution_limit"
	reasonBitrateLimit           = "bitrate_limit"
	reasonHDRNotSupported        = "hdr_not_supported"
)

type sourceCandidate struct {
	sourceIndex    int
	assetIndex     int
	directHint     bool
	resolutionHint int
}

type sourcePlan struct {
	candidate  sourceCandidate
	inspection MediaInspection
	mode       string
	decision   *PlaybackDecision
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

	preferDirect := capabilities.PreferDirectPlay == nil || *capabilities.PreferDirectPlay
	conversionDenied := false
	capabilityMissing := false
	var copyPlan sourcePlan
	var transcodePlan sourcePlan
	var fallbackCopyPlan sourcePlan
	var fallbackTranscodePlan sourcePlan
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
		if mode == "" {
			if !allowTranscoding && transcodingRequired(inspection, capabilities) {
				conversionDenied = true
			} else {
				capabilityMissing = true
			}
			continue
		}
		if mode == processingTranscodeAudio || mode == processingTranscode {
			if !allowTranscoding {
				conversionDenied = true
				continue
			}
		}

		plan := sourcePlan{candidate: candidate, inspection: inspection, mode: mode, decision: decision}
		video := primaryTrack(inspection.VideoTracks)
		mislabeled := candidate.resolutionHint > 0 && video != nil && video.Height < candidate.resolutionHint*9/10
		if mislabeled {
			if video.Height <= 0 {
				continue
			}
			if mode == "direct" || mode == processingRemux {
				if betterCopyPlan(plan, fallbackCopyPlan, preferDirect, true) {
					fallbackCopyPlan = plan
				}
			} else if fallbackTranscodePlan.mode == "" || video.Height > planVideoHeight(fallbackTranscodePlan) {
				fallbackTranscodePlan = plan
			}
			continue
		}

		if mode == "direct" || mode == processingRemux {
			if betterCopyPlan(plan, copyPlan, preferDirect, false) {
				copyPlan = plan
			}
		} else if transcodePlan.mode == "" {
			transcodePlan = plan
		}
	}

	selected := copyPlan
	if selected.mode == "" {
		selected = fallbackCopyPlan
	}
	if selected.mode == "" {
		selected = transcodePlan
	}
	if selected.mode == "" {
		selected = fallbackTranscodePlan
	}
	if selected.mode != "" {
		applyPlaybackDecision(sources, assets, selected.candidate, selected.inspection, selected.mode, selected.decision, capabilities)
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

func betterCopyPlan(candidate, current sourcePlan, preferDirect, useInspectedHeight bool) bool {
	if current.mode == "" {
		return true
	}
	candidateQuality := candidate.candidate.resolutionHint
	currentQuality := current.candidate.resolutionHint
	if useInspectedHeight || candidateQuality == currentQuality {
		candidateQuality = planVideoHeight(candidate)
		currentQuality = planVideoHeight(current)
	}
	if candidateQuality != currentQuality {
		return candidateQuality > currentQuality
	}
	return preferDirect && candidate.mode == "direct" && current.mode == processingRemux
}

func planVideoHeight(plan sourcePlan) int {
	if video := primaryTrack(plan.inspection.VideoTracks); video != nil {
		return video.Height
	}
	return 0
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
		source.DolbyVisionProfile = video.DolbyVisionProfile
		source.DolbyVisionBLPresent = video.DolbyVisionBLPresent
		source.DolbyVisionCompatibilityID = video.DolbyVisionCompatibilityID
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
	if video := primaryTrack(inspection.VideoTracks); video != nil {
		asset.VideoBitDepth = video.BitDepth
	}
	if decision != nil {
		asset.ToneMap = decision.ToneMapping
		asset.DolbyVisionToneMapSafe = decision.Source == nil ||
			!strings.EqualFold(strings.TrimSpace(decision.Source.HDRFormat), "dolby_vision") ||
			dolbyVisionBaseHDRFormat(decision.Source.DolbyVisionProfile, decision.Source.DolbyVisionBLPresent, decision.Source.DolbyVisionCompatibilityID) != ""
		if decision.Target != nil {
			asset.TargetHeight = decision.Target.Height
			asset.VideoBitrateKbps = decision.Target.VideoBitrateKbps
			if decision.VideoAction == "transcode" {
				asset.TargetVideoCodec = canonicalTargetVideoCodec(decision.Target.VideoCodec)
				asset.QualityPreset = transcodeQualityPreset(capabilities)
				if asset.TargetVideoCodec == "hevc" || asset.TargetVideoCodec == "av1" {
					asset.HLSSegmentContainer = "mp4"
				}
			}
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
	reasons := directIncompatibilityReasons(source, inspection, capabilities)
	if remuxSupported(inspection, capabilities) {
		return processingRemux, processingDecisionWithReasons(decisionRemuxRequired, reasons, "copy", "copy", inspection, capabilities, false)
	}
	if audioTranscodeSupported(inspection, capabilities) {
		return processingTranscodeAudio, processingDecisionWithReasons(decisionAudioTranscodeRequired, reasons, "copy", "transcode", inspection, capabilities, false)
	}
	if fullTranscodeSupported(capabilities) {
		targetAudio := &MediaTrack{Codec: "aac", Channels: targetAudioChannels(capabilities)}
		toneMap := videoTranscodeNeedsToneMapping(inspection, capabilities, targetAudio)
		if toneMap && !genericToneMappingSupported(inspection) {
			return "", nil
		}
		return processingTranscode, processingDecisionWithReasons(decisionVideoTranscodeRequired, reasons, "transcode", "transcode", inspection, capabilities, toneMap)
	}
	return "", nil
}

func directPlaybackSupported(source Source, inspection MediaInspection, video, audio *MediaTrack, capabilities Capabilities) bool {
	container := source.Container
	if inspection.Container != "" {
		container = inspection.Container
	}
	if capabilities.AllowDirectPassthrough {
		return supports(capabilities.StreamingProtocols, source.Protocol) && supportsContainer(capabilities.Containers, container)
	}
	return supports(capabilities.StreamingProtocols, source.Protocol) &&
		containerProfileConditionsSupported(container, inspection, capabilities.ContainerProfiles) &&
		directMediaProfileSupported(container, video, audio, capabilities) &&
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
		return processingCopyMediaProfileSupported("mp4", video, nil, capabilities)
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
	return processingCopyMediaProfileSupported("mp4", video, targetAudio, capabilities)
}

func fullTranscodeSupported(capabilities Capabilities) bool {
	if !requestedProcessingMode(capabilities.ProcessingModes, processingTranscode) || !processingOutputSupported(capabilities) {
		return false
	}
	return selectedTranscodeVideoCodec(capabilities) != ""
}

func processingDecision(reason, videoAction, audioAction string, inspection MediaInspection, capabilities Capabilities, toneMap bool) *PlaybackDecision {
	return processingDecisionWithReasons(reason, nil, videoAction, audioAction, inspection, capabilities, toneMap)
}

func processingDecisionWithReasons(reason string, reasons []string, videoAction, audioAction string, inspection MediaInspection, capabilities Capabilities, toneMap bool) *PlaybackDecision {
	protocol, container := processingOutput()
	target := &PlaybackDecisionTarget{Protocol: protocol, Container: container}
	video := primaryTrack(inspection.VideoTracks)
	audio := primaryTrack(inspection.AudioTracks)
	var targetAudio *MediaTrack
	if audioAction == "copy" {
		targetAudio = audio
	} else if audio != nil {
		targetAudio = &MediaTrack{Codec: "aac", Channels: targetAudioChannels(capabilities)}
	}
	if videoAction == "copy" && video != nil {
		target.VideoCodec = normalizedCodec(video.Codec)
		target.Height = video.Height
		target.VideoBitDepth = video.BitDepth
		target.VideoBitrateKbps = video.BitrateKbps
	} else {
		target.VideoCodec, target.VideoBitDepth = selectedTranscodeVideoTarget(capabilities, inspection, targetAudio)
		if target.VideoCodec == "" {
			target.VideoCodec = "h264"
			target.VideoBitDepth = 8
		}
		if video != nil {
			target.Height = video.Height
		}
		if sourceHDRNeedsBitDepthToneMap(inspection, target.VideoBitDepth) {
			toneMap = true
		}
		maximumHeight := capabilities.MaximumHeight
		if toneMap && capabilities.ToneMapMaximumHeight > 0 && (maximumHeight == 0 || capabilities.ToneMapMaximumHeight < maximumHeight) {
			maximumHeight = capabilities.ToneMapMaximumHeight
		}
		if maximumHeight > 0 && (target.Height == 0 || target.Height > maximumHeight) {
			target.Height = maximumHeight
		}
		target.VideoBitrateKbps = transcodeVideoBitrateKbps(capabilities)
	}
	if targetAudio != nil {
		target.AudioCodec = normalizedCodec(targetAudio.Codec)
	}
	decision := &PlaybackDecision{
		Reason: reason, Reasons: append([]string(nil), reasons...), VideoAction: videoAction, AudioAction: audioAction,
		SubtitleAction: "none", ToneMapping: toneMap, Source: decisionSource(inspection), Target: target,
	}
	if videoAction == "transcode" {
		decision.Pipeline = plannedPlaybackPipeline(inspection, capabilities, target, toneMap, false)
	}
	return decision
}

func selectedTranscodeVideoCodec(capabilities Capabilities) string {
	return selectedTranscodeVideoCodecForAudio(capabilities, &MediaTrack{Codec: "aac", Channels: targetAudioChannels(capabilities)})
}

func selectedTranscodeVideoCodecForAudio(capabilities Capabilities, audio *MediaTrack) string {
	preferred := canonicalTargetVideoCodec(capabilities.transcodeCapabilities.PreferredVideoCodec)
	candidates := [...]string{preferred, "h264", "hevc", "av1"}
	for index, codec := range candidates {
		codec = canonicalTargetVideoCodec(codec)
		if codec == "" || codec == "auto" {
			continue
		}
		duplicate := false
		for prior := range index {
			if canonicalTargetVideoCodec(candidates[prior]) == codec {
				duplicate = true
				break
			}
		}
		if duplicate || (codec == "hevc" || codec == "av1") && normalizedHLSSegmentContainer(capabilities.HLSSegmentContainer) != "mp4" {
			continue
		}
		if !engineCanEncode(capabilities.transcodeCapabilities.EncodeCodecs, codec) {
			continue
		}
		if processingMediaProfileSupported("mp4", &MediaTrack{Codec: codec}, audio, capabilities) {
			return codec
		}
	}
	return ""
}

func selectedTranscodeVideoTarget(capabilities Capabilities, inspection MediaInspection, audio *MediaTrack) (string, int) {
	codec := selectedTranscodeVideoCodecForAudio(capabilities, audio)
	video := primaryTrack(inspection.VideoTracks)
	needsScale := video != nil && capabilities.MaximumHeight > 0 && video.Height > capabilities.MaximumHeight
	if codec != "hevc" || video == nil || video.BitDepth <= 8 || needsScale || videoNeedsToneMapping(inspection, capabilities) || !capabilities.transcodeCapabilities.HEVCMain10 {
		return codec, 8
	}
	for _, profile := range capabilities.MediaProfiles {
		if transcodeMediaProfileSupportsBitDepth(profile, codec, audio, 10) {
			return codec, 10
		}
	}
	return codec, 8
}

func transcodeMediaProfileSupportsBitDepth(profile MediaProfile, codec string, audio *MediaTrack, bitDepth int) bool {
	containers := profile.Container
	if profile.ContainersCSV != "" {
		containers = profile.ContainersCSV
	}
	maximumBitDepth := profile.MaximumVideoBitDepth
	if maximumBitDepth == 0 {
		maximumBitDepth = 8
	}
	if maximumBitDepth < bitDepth || profile.DirectPlay && !profile.Transcoding ||
		!mediaProfileContainerMatches(containers, "mp4") || normalizedCodec(profile.VideoCodec) != normalizedCodec(codec) {
		return false
	}
	audioCodecs := profile.AudioCodec
	if profile.AudioCodecsCSV != "" {
		audioCodecs = profile.AudioCodecsCSV
	}
	return audio == nil || mediaProfileCodecMatches(audioCodecs, audio.Codec)
}

func sourceHDRNeedsBitDepthToneMap(inspection MediaInspection, targetBitDepth int) bool {
	format := strings.ToLower(strings.TrimSpace(inspection.HDRFormat))
	if format == "dolby_vision" {
		if video := primaryTrack(inspection.VideoTracks); video != nil {
			if baseFormat := dolbyVisionBaseHDRFormat(video.DolbyVisionProfile, video.DolbyVisionBLPresent, video.DolbyVisionCompatibilityID); baseFormat != "" {
				format = baseFormat
			}
		}
	}
	return targetBitDepth <= 8 && format != "" && format != "sdr"
}
func videoTranscodeNeedsToneMapping(inspection MediaInspection, capabilities Capabilities, audio *MediaTrack) bool {
	_, targetBitDepth := selectedTranscodeVideoTarget(capabilities, inspection, audio)
	return videoNeedsToneMapping(inspection, capabilities) || sourceHDRNeedsBitDepthToneMap(inspection, targetBitDepth)
}

func canonicalTargetVideoCodec(codec string) string {
	switch normalizedCodec(codec) {
	case "h264":
		return "h264"
	case "h265":
		return "hevc"
	case "av1":
		return "av1"
	case "auto":
		return "auto"
	default:
		return ""
	}
}

func engineCanEncode(codecs []string, codec string) bool {
	if len(codecs) == 0 {
		return codec == "h264"
	}
	for _, candidate := range codecs {
		if canonicalTargetVideoCodec(candidate) == codec {
			return true
		}
	}
	return false
}

func directIncompatibilityReasons(source Source, inspection MediaInspection, capabilities Capabilities) []string {
	video := primaryTrack(inspection.VideoTracks)
	audio := primaryTrack(inspection.AudioTracks)
	container := inspection.Container
	if container == "" {
		container = source.Container
	}
	reasons := make([]string, 0, 6)
	add := func(reason string) {
		for _, existing := range reasons {
			if existing == reason {
				return
			}
		}
		reasons = append(reasons, reason)
	}
	if !supports(capabilities.StreamingProtocols, source.Protocol) || (len(capabilities.MediaProfiles) == 0 && !supportsContainer(capabilities.Containers, container)) {
		add(reasonContainerNotSupported)
	}
	if video != nil {
		if (len(capabilities.MediaProfiles) == 0 && !supportsCodec(capabilities.VideoCodecs, video.Codec)) || !videoCodecConditionsSupported(video, capabilities) {
			add(reasonVideoCodecNotSupported)
		}
		if capabilities.MaximumHeight > 0 && (video.Height <= 0 || video.Height > capabilities.MaximumHeight) {
			add(reasonResolutionLimit)
		}
		if capabilities.MaximumVideoBitrateKbps > 0 && (video.BitrateKbps <= 0 || video.BitrateKbps > capabilities.MaximumVideoBitrateKbps) {
			add(reasonBitrateLimit)
		}
		if !clientSupportsVideoHDR(video, inspection.HDRFormat, capabilities) {
			add(reasonHDRNotSupported)
		}
	}
	if audio != nil && len(capabilities.MediaProfiles) == 0 && (!supportsCodec(capabilities.AudioCodecs, audio.Codec) || !audioWithinClientLimits(audio, capabilities)) {
		add(reasonAudioCodecNotSupported)
	}
	if video != nil && len(capabilities.MediaProfiles) > 0 {
		containerSupported, videoSupported, audioSupported := directProfileCompatibility(container, video, audio, capabilities)
		if !containerSupported {
			add(reasonContainerNotSupported)
		}
		if !videoSupported {
			add(reasonVideoCodecNotSupported)
		}
		if !audioSupported {
			add(reasonAudioCodecNotSupported)
		}
	}
	return reasons
}

func directProfileCompatibility(container string, video, audio *MediaTrack, capabilities Capabilities) (bool, bool, bool) {
	containerSupported := false
	videoSupported := false
	audioSupported := audio == nil
	for _, profile := range capabilities.MediaProfiles {
		if (profile.DirectPlay || profile.Transcoding) && !profile.DirectPlay {
			continue
		}
		containers := profile.Container
		if profile.ContainersCSV != "" {
			containers = profile.ContainersCSV
		}
		if !mediaProfileContainerMatches(containers, container) {
			continue
		}
		containerSupported = true
		videoMatches := video == nil || normalizedCodec(profile.VideoCodec) == normalizedCodec(video.Codec) && mediaProfileVideoConditionsSupported(profile, video)
		audioCodecs := profile.AudioCodec
		if profile.AudioCodecsCSV != "" {
			audioCodecs = profile.AudioCodecsCSV
		}
		audioMatches := audio == nil || mediaProfileCodecMatches(audioCodecs, audio.Codec)
		if videoMatches && audioMatches {
			videoSupported = true
			audioSupported = true
		}
	}
	return containerSupported, videoSupported, audioSupported
}

func plannedPlaybackPipeline(inspection MediaInspection, capabilities Capabilities, target *PlaybackDecisionTarget, toneMap, subtitleBurn bool) *PlaybackPipeline {
	backend := strings.ToLower(strings.TrimSpace(capabilities.transcodeCapabilities.HardwareAcceleration))
	if backend == "" || backend == "auto" {
		backend = "software"
	}
	executionBackend := backend
	if executionBackend == "hybrid" {
		executionBackend = "vaapi"
	}
	targetCodec := "h264"
	if target != nil && target.VideoCodec != "" {
		targetCodec = target.VideoCodec
	}
	pipeline := &PlaybackPipeline{
		HardwareAcceleration: backend,
		Decoder:              "software",
		Encoder:              targetVideoEncoder(targetCodec, executionBackend),
	}
	video := primaryTrack(inspection.VideoTracks)
	hardwareDecode := executionBackend != "software" && !subtitleBurn && video != nil &&
		(supportsCodec(capabilities.transcodeCapabilities.DecodeCodecs, video.Codec) ||
			canonicalTargetVideoCodec(video.Codec) == "hevc" && video.BitDepth > 8 && target != nil && target.VideoBitDepth >= 10 && capabilities.transcodeCapabilities.HEVCMain10)
	needsScale := target != nil && target.Height > 0 && video != nil && video.Height > target.Height
	if needsScale && executionBackend != "vaapi" && executionBackend != "qsv" {
		hardwareDecode = false
	}
	if toneMap {
		pipeline.ToneMapBackend = strings.ToLower(strings.TrimSpace(capabilities.toneMapBackend))
		if pipeline.ToneMapBackend == "" {
			pipeline.ToneMapBackend = "software"
		}
		if (pipeline.ToneMapBackend == "software" || pipeline.ToneMapBackend == "hybrid") &&
			(executionBackend != "vaapi" || video == nil || canonicalTargetVideoCodec(video.Codec) != "hevc" || video.BitDepth != 10) {
			hardwareDecode = false
		}
	}
	if hardwareDecode {
		switch executionBackend {
		case "amf":
			pipeline.Decoder = "d3d11va"
		case "nvenc":
			pipeline.Decoder = "cuda"
		default:
			pipeline.Decoder = executionBackend
		}
	}
	pipeline.ZeroCopy = hardwareDecode && (!toneMap || pipeline.ToneMapBackend != "software" && pipeline.ToneMapBackend != "hybrid")
	return pipeline
}

func targetVideoEncoder(codec, backend string) string {
	codec = canonicalTargetVideoCodec(codec)
	if backend == "" || backend == "auto" || backend == "software" {
		switch codec {
		case "hevc":
			return "libx265"
		case "av1":
			return "libsvtav1"
		default:
			return "libx264"
		}
	}
	if backend == "nvenc" {
		switch codec {
		case "hevc":
			return "hevc_nvenc"
		case "av1":
			return "av1_nvenc"
		default:
			return "h264_nvenc"
		}
	}
	return codec + "_" + backend
}

func transcodeQualityPreset(capabilities Capabilities) string {
	switch strings.ToLower(strings.TrimSpace(capabilities.transcodeCapabilities.QualityPreset)) {
	case "speed":
		return "speed"
	case "quality":
		return "quality"
	default:
		return "balanced"
	}
}

func appendDecisionReason(decision *PlaybackDecision, reason string) {
	if decision == nil || reason == "" {
		return
	}
	for _, existing := range decision.Reasons {
		if existing == reason {
			return
		}
	}
	decision.Reasons = append(decision.Reasons, reason)
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
	return videoCodecConditionsSupported(video, capabilities) && clientSupportsVideoHDR(video, hdrFormat, capabilities)
}

func clientSupportsHDR(format string, capabilities Capabilities) bool {
	format = strings.ToLower(strings.TrimSpace(format))
	return format == "" || format == "sdr" || len(capabilities.HDRFormats) > 0 && supports(capabilities.HDRFormats, format)
}

func dolbyVisionBaseHDRFormat(profile int, baseLayerPresent bool, compatibilityID int) string {
	if profile == 5 || !baseLayerPresent {
		return ""
	}
	switch compatibilityID {
	case 1:
		return "hdr10"
	case 2:
		return "sdr"
	case 4:
		return "hlg"
	default:
		return ""
	}
}

func clientSupportsVideoHDR(video *MediaTrack, format string, capabilities Capabilities) bool {
	format = strings.ToLower(strings.TrimSpace(format))
	if clientSupportsHDR(format, capabilities) {
		return true
	}
	if format == "dolby_vision" {
		if video == nil {
			return false
		}
		format = dolbyVisionBaseHDRFormat(video.DolbyVisionProfile, video.DolbyVisionBLPresent, video.DolbyVisionCompatibilityID)
		if format == "" {
			return false
		}
		if clientSupportsHDR(format, capabilities) {
			return true
		}
	}
	switch format {
	case "hdr10", "hlg":
		for _, profile := range capabilities.MediaProfiles {
			if profile.DirectPlay && profile.SupportsNonDolbyVisionHDR && video != nil && normalizedCodec(profile.VideoCodec) == normalizedCodec(video.Codec) {
				return true
			}
		}
	}
	return false
}

func videoNeedsToneMapping(inspection MediaInspection, capabilities Capabilities) bool {
	return !clientSupportsVideoHDR(primaryTrack(inspection.VideoTracks), inspection.HDRFormat, capabilities)
}

func videoCodecConditionsSupported(video *MediaTrack, capabilities Capabilities) bool {
	for _, profile := range capabilities.MediaProfiles {
		if !profile.DirectPlay || video == nil || normalizedCodec(profile.VideoCodec) != normalizedCodec(video.Codec) {
			continue
		}
		if !mediaProfileVideoConditionsSupported(profile, video) {
			return false
		}
	}
	return true
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
	return video != nil && containerProfileConditionsSupported(inspection.Container, inspection, capabilities.ContainerProfiles) &&
		directMediaProfileSupported(inspection.Container, video, primaryTrack(inspection.AudioTracks), capabilities) &&
		videoWithinClientLimits(video, inspection.HDRFormat, capabilities) && audioWithinClientLimits(primaryTrack(inspection.AudioTracks), capabilities)
}

func containerProfileConditionsSupported(container string, inspection MediaInspection, profiles []ContainerProfile) bool {
	video := primaryTrack(inspection.VideoTracks)
	for _, profile := range profiles {
		if !mediaProfileContainerMatches(profile.ContainersCSV, container) {
			continue
		}
		for _, condition := range profile.Conditions {
			if !containerConditionSatisfied(condition, video, inspection) {
				return false
			}
		}
	}
	return true
}

func containerConditionSatisfied(condition ProfileCondition, video *MediaTrack, inspection MediaInspection) bool {
	property := strings.ToLower(strings.TrimSpace(condition.Property))
	switch property {
	case "width":
		return numericConditionSatisfied(condition, trackInteger(video, func(track *MediaTrack) int64 { return int64(track.Width) }))
	case "height":
		return numericConditionSatisfied(condition, trackInteger(video, func(track *MediaTrack) int64 { return int64(track.Height) }))
	case "videolevel":
		return numericConditionSatisfied(condition, trackInteger(video, func(track *MediaTrack) int64 { return int64(track.Level) }))
	case "videobitrate":
		return numericConditionSatisfied(condition, trackInteger(video, func(track *MediaTrack) int64 { return int64(track.BitrateKbps) * 1000 }))
	case "videoprofile":
		current, known := trackString(video, func(track *MediaTrack) string { return track.Profile })
		return stringConditionSatisfied(condition, current, known)
	case "videorangetype":
		current, known := trackString(video, func(track *MediaTrack) string { return track.VideoRangeType })
		return stringConditionSatisfied(condition, current, known)
	case "numstreams":
		return numericConditionSatisfied(condition, knownInteger(int64(len(inspection.VideoTracks)+len(inspection.AudioTracks)+len(inspection.SubtitleTracks))))
	case "numvideostreams":
		return numericConditionSatisfied(condition, knownInteger(int64(len(inspection.VideoTracks))))
	case "numaudiostreams":
		return numericConditionSatisfied(condition, knownInteger(int64(len(inspection.AudioTracks))))
	default:
		return false
	}
}

type conditionInteger struct {
	value int64
	known bool
}

func knownInteger(value int64) conditionInteger {
	return conditionInteger{value: value, known: true}
}

func trackInteger(track *MediaTrack, value func(*MediaTrack) int64) conditionInteger {
	if track == nil {
		return conditionInteger{}
	}
	current := value(track)
	return conditionInteger{value: current, known: current > 0}
}

func trackString(track *MediaTrack, value func(*MediaTrack) string) (string, bool) {
	if track == nil {
		return "", false
	}
	current := strings.TrimSpace(value(track))
	return current, current != ""
}

func numericConditionSatisfied(condition ProfileCondition, current conditionInteger) bool {
	if !current.known {
		return !condition.Required
	}
	operator := strings.ToLower(strings.TrimSpace(condition.Condition))
	if operator == "equalsany" {
		for _, candidate := range strings.Split(condition.Value, "|") {
			expected, err := strconv.ParseInt(strings.TrimSpace(candidate), 10, 64)
			if err == nil && current.value == expected {
				return true
			}
		}
		return false
	}
	expected, err := strconv.ParseInt(strings.TrimSpace(condition.Value), 10, 64)
	if err != nil {
		return false
	}
	switch operator {
	case "equals":
		return current.value == expected
	case "notequals":
		return current.value != expected
	case "lessthanequal":
		return current.value <= expected
	case "greaterthanequal":
		return current.value >= expected
	default:
		return false
	}
}

func stringConditionSatisfied(condition ProfileCondition, current string, known bool) bool {
	if !known {
		return !condition.Required
	}
	switch strings.ToLower(strings.TrimSpace(condition.Condition)) {
	case "equals":
		return strings.EqualFold(current, strings.TrimSpace(condition.Value))
	case "notequals":
		return !strings.EqualFold(current, strings.TrimSpace(condition.Value))
	case "equalsany":
		for _, expected := range strings.Split(condition.Value, "|") {
			if strings.EqualFold(current, strings.TrimSpace(expected)) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

const (
	mediaProfileAny = iota
	mediaProfileDirect
	mediaProfileTranscoding
)

func mediaProfileSupported(container string, video, audio *MediaTrack, capabilities Capabilities) bool {
	return mediaProfileSupportedFor(container, video, audio, capabilities, mediaProfileAny, true)
}

func directMediaProfileSupported(container string, video, audio *MediaTrack, capabilities Capabilities) bool {
	return mediaProfileSupportedFor(container, video, audio, capabilities, mediaProfileDirect, true)
}

func processingMediaProfileSupported(container string, video, audio *MediaTrack, capabilities Capabilities) bool {
	return mediaProfileSupportedFor(container, video, audio, capabilities, mediaProfileTranscoding, false)
}

func processingCopyMediaProfileSupported(container string, video, audio *MediaTrack, capabilities Capabilities) bool {
	return mediaProfileSupportedFor(container, video, audio, capabilities, mediaProfileTranscoding, true)
}

func mediaProfileSupportedFor(container string, video, audio *MediaTrack, capabilities Capabilities, profileUse int, applyConditions bool) bool {
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
		hasExplicitUse := profile.DirectPlay || profile.Transcoding
		if hasExplicitUse && (profileUse == mediaProfileDirect && !profile.DirectPlay || profileUse == mediaProfileTranscoding && !profile.Transcoding) {
			continue
		}
		containers := profile.Container
		if profile.ContainersCSV != "" {
			containers = profile.ContainersCSV
		}
		if container != "" && !mediaProfileContainerMatches(containers, container) {
			continue
		}
		if video == nil || normalizedCodec(profile.VideoCodec) != normalizedCodec(video.Codec) {
			continue
		}
		if applyConditions && !mediaProfileVideoConditionsSupported(profile, video) {
			continue
		}
		audioCodecs := profile.AudioCodec
		if profile.AudioCodecsCSV != "" {
			audioCodecs = profile.AudioCodecsCSV
		}
		if audio == nil || mediaProfileCodecMatches(audioCodecs, audio.Codec) {
			return true
		}
	}
	return false
}

func mediaProfileVideoConditionsSupported(profile MediaProfile, video *MediaTrack) bool {
	if video == nil || profile.RequiredConditionUnknown {
		return false
	}
	if profile.MaximumVideoLevel > 0 {
		if video.Level <= 0 {
			if profile.VideoLevelRequired {
				return false
			}
		} else if video.Level > profile.MaximumVideoLevel {
			return false
		}
	}
	maximumBitDepth := profile.MaximumVideoBitDepth
	if maximumBitDepth == 0 {
		maximumBitDepth = 8
	}
	if video.BitDepth > maximumBitDepth || profile.MaximumVideoBitDepth > 0 && video.BitDepth <= 0 {
		return false
	}
	if profile.ExcludedVideoRange != "" {
		videoRange := strings.TrimSpace(video.VideoRangeType)
		if videoRange == "" {
			if profile.VideoRangeRequired {
				return false
			}
		} else if strings.EqualFold(videoRange, profile.ExcludedVideoRange) {
			return false
		}
	}
	return true
}

func mediaProfileContainerMatches(profile, candidate string) bool {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if candidate == "m4v" || candidate == "mov" {
		candidate = "mp4"
	} else if candidate == "m3u8" {
		candidate = "hls"
	}
	for _, value := range strings.Split(profile, ",") {
		if strings.ToLower(strings.TrimSpace(value)) == candidate {
			return true
		}
	}
	return false
}

func mediaProfileCodecMatches(profile, candidate string) bool {
	candidate = normalizedCodec(candidate)
	for _, value := range strings.Split(profile, ",") {
		if normalizedCodec(value) == candidate {
			return true
		}
	}
	return false
}

func compatibleRemuxAudioTrack(video MediaTrack, tracks []MediaTrack, capabilities Capabilities) *MediaTrack {
	for index := range tracks {
		if mp4RemuxableAudio(tracks[index].Codec) && audioWithinClientLimits(&tracks[index], capabilities) &&
			processingCopyMediaProfileSupported("mp4", &video, &tracks[index], capabilities) {
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
		return false
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
