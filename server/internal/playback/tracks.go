package playback

import (
	"strconv"
	"strings"
)

const (
	assetKindEmbeddedSubtitle  = "embedded_subtitle"
	assetKindConvertedSubtitle = "converted_subtitle"
	assetKindBitmapSubtitle    = "bitmap_subtitle"
	subtitleBurnText           = "text"
	subtitleBurnBitmap         = "bitmap"
)

func applyPlaybackPreferences(sources []Source, assets []storedAsset, input ResolveInput) error {
	for sourceIndex := range sources {
		source := &sources[sourceIndex]
		if !source.Compatible || source.Media == nil {
			continue
		}
		assetIndex := storedAssetIndex(assets, source.ID)
		if assetIndex < 0 {
			return nil
		}
		inspection := *source.Media
		track, explicit := preferredAudioTrack(inspection.AudioTracks, input.PreferredAudioTrack, input.PreferredAudioLanguage)
		if explicit && track == nil {
			return ErrInvalidInput
		}
		if track == nil {
			return nil
		}
		primary := primaryTrack(inspection.AudioTracks)
		video := primaryTrack(inspection.VideoTracks)
		trackCopySupported := video != nil && mp4RemuxableAudio(track.Codec) &&
			audioWithinClientLimits(track, input.Capabilities) &&
			processingMediaProfileSupported("mp4", video, track, input.Capabilities)
		requiresTrackSwitch := primary != nil && primary.Index != track.Index

		if source.Mode == processingRemux && !trackCopySupported && !explicit && video != nil {
			if compatible := compatibleRemuxAudioTrack(*video, inspection.AudioTracks, input.Capabilities); compatible != nil {
				track = compatible
				trackCopySupported = true
				requiresTrackSwitch = primary != nil && primary.Index != track.Index
			}
		}
		selectedInspection := inspectionWithSelectedAudio(inspection, track)
		reasons := selectedAudioDecisionReasons(source.Decision, selectedInspection, input.Capabilities)
		if source.Mode == "direct" && requiresTrackSwitch && trackCopySupported &&
			requestedProcessingMode(input.Capabilities.ProcessingModes, processingRemux) && processingOutputSupported(input.Capabilities) {
			decision := processingDecisionWithReasons(decisionRemuxRequired, reasons, "copy", "copy", selectedInspection, input.Capabilities, false)
			applyPlaybackDecision(sources, assets, sourceCandidate{sourceIndex: sourceIndex, assetIndex: assetIndex}, inspection, processingRemux, decision, input.Capabilities)
			source = &sources[sourceIndex]
		} else if (source.Mode == "direct" && requiresTrackSwitch || source.Mode == processingRemux) && !trackCopySupported {
			mode := ""
			var decision *PlaybackDecision
			switch {
			case audioTranscodeSupported(selectedInspection, input.Capabilities):
				mode = processingTranscodeAudio
				decision = processingDecisionWithReasons(decisionAudioTranscodeRequired, reasons, "copy", "transcode", selectedInspection, input.Capabilities, false)
			case fullTranscodeSupported(input.Capabilities):
				mode = processingTranscode
				targetAudio := &MediaTrack{Codec: "aac", Channels: targetAudioChannels(input.Capabilities)}
				toneMap := videoTranscodeNeedsToneMapping(selectedInspection, input.Capabilities, targetAudio)
				decision = processingDecisionWithReasons(decisionVideoTranscodeRequired, reasons, "transcode", "transcode", selectedInspection, input.Capabilities, toneMap)
			default:
				if !input.AllowTranscoding {
					return ErrTranscodingDisabled
				}
				return ErrClientCapabilityMissing
			}
			if !input.AllowTranscoding {
				return ErrTranscodingDisabled
			}
			if decision != nil && decision.ToneMapping && !genericToneMappingSupported(selectedInspection) {
				return ErrUnsupportedSource
			}
			applyPlaybackDecision(sources, assets, sourceCandidate{sourceIndex: sourceIndex, assetIndex: assetIndex}, inspection, mode, decision, input.Capabilities)
			source = &sources[sourceIndex]
		} else if source.Mode == "direct" && requiresTrackSwitch {
			return ErrClientCapabilityMissing
		}
		index := track.Index
		assets[assetIndex].AudioTrackIndex = &index
		refreshSelectedAudioDecision(source, &assets[assetIndex], selectedInspection, input.Capabilities)
		return nil
	}
	return nil
}

func selectedAudioDecisionReasons(decision *PlaybackDecision, inspection MediaInspection, capabilities Capabilities) []string {
	var existing []string
	if decision != nil {
		existing = decision.Reasons
	}
	reasons := make([]string, 0, len(existing)+1)
	for _, reason := range existing {
		if reason != reasonAudioCodecNotSupported && !supports(reasons, reason) {
			reasons = append(reasons, reason)
		}
	}
	if !selectedAudioDirectlySupported(inspection, capabilities) {
		reasons = append(reasons, reasonAudioCodecNotSupported)
	}
	return reasons
}

func selectedAudioDirectlySupported(inspection MediaInspection, capabilities Capabilities) bool {
	audio := primaryTrack(inspection.AudioTracks)
	if audio == nil {
		return true
	}
	if len(capabilities.MediaProfiles) == 0 {
		return audioWithinClientLimits(audio, capabilities)
	}
	_, _, audioSupported := directProfileCompatibility(inspection.Container, primaryTrack(inspection.VideoTracks), audio, capabilities)
	return audioSupported
}

func refreshSelectedAudioDecision(source *Source, asset *storedAsset, inspection MediaInspection, capabilities Capabilities) {
	if source == nil || asset == nil || source.Decision == nil {
		return
	}
	decision := clonePlaybackDecision(source.Decision)
	decision.Reasons = selectedAudioDecisionReasons(decision, inspection, capabilities)
	decision.Source = decisionSource(inspection)
	if decision.Target != nil && decision.AudioAction == "copy" {
		if audio := primaryTrack(inspection.AudioTracks); audio != nil {
			decision.Target.AudioCodec = normalizedCodec(audio.Codec)
		} else {
			decision.Target.AudioCodec = ""
		}
	}
	source.Decision = decision
	asset.Decision = clonePlaybackDecision(decision)
}

func preferredAudioTrack(tracks []MediaTrack, explicitIndex *int, language string) (*MediaTrack, bool) {
	if explicitIndex != nil {
		for index := range tracks {
			if tracks[index].Index == *explicitIndex {
				return &tracks[index], true
			}
		}
		return nil, true
	}
	for index := range tracks {
		if languageMatches(tracks[index].Language, language) {
			return &tracks[index], false
		}
	}
	return primaryTrack(tracks), false
}

func embeddedSubtitles(sources []Source, assets []storedAsset, capabilityValues ...Capabilities) ([]Subtitle, []storedAsset) {
	capabilities := Capabilities{}
	if len(capabilityValues) > 0 {
		capabilities = capabilityValues[0]
	}
	for sourceIndex := range sources {
		source := &sources[sourceIndex]
		if !source.Compatible || source.Media == nil {
			continue
		}
		assetIndex := storedAssetIndex(assets, source.ID)
		if assetIndex < 0 {
			return nil, nil
		}
		sourceAsset := assets[assetIndex]
		subtitles := make([]Subtitle, 0, len(source.Media.SubtitleTracks))
		subtitleAssets := make([]storedAsset, 0, len(source.Media.SubtitleTracks))
		for ordinal, track := range source.Media.SubtitleTracks {
			kind := assetKindEmbeddedSubtitle
			delivery := "external"
			burnType := ""
			switch {
			case webVTTConvertibleSubtitle(track.Codec) &&
				(len(capabilities.SubtitleModes) == 0 || requestedProcessingMode(capabilities.SubtitleModes, "external")):
			case textSubtitle(track.Codec) && requestedProcessingMode(capabilities.SubtitleModes, "burn"):
				burnType = subtitleBurnText
				delivery = "burn"
			case bitmapSubtitle(track.Codec) && requestedProcessingMode(capabilities.SubtitleModes, "burn"):
				kind = assetKindBitmapSubtitle
				burnType = subtitleBurnBitmap
				delivery = "burn"
			default:
				continue
			}
			trackIndex := track.Index
			id := "embedded-subtitle-" + strconv.Itoa(track.Index)
			if delivery == "external" {
				id += ".vtt"
			}
			subtitles = append(subtitles, Subtitle{
				ID: id, AddonID: source.AddonID, ManifestID: source.ManifestID,
				Language: track.Language, Forced: track.Forced, Delivery: delivery,
			})
			subtitleAssets = append(subtitleAssets, storedAsset{
				ID: id, Kind: kind, URL: sourceAsset.URL, Container: sourceAsset.Container,
				Headers: sourceAsset.Headers, SubtitleTrackIndex: &trackIndex,
				SubtitleTrackType: burnType, SubtitleTrackOrdinal: ordinal,
			})
		}
		return subtitles, subtitleAssets
	}
	return nil, nil
}

func webVTTConvertibleSubtitle(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "ass", "ssa", "subrip", "srt", "text", "webvtt", "mov_text":
		return true
	default:
		return false
	}
}

func textSubtitle(codec string) bool {
	return webVTTConvertibleSubtitle(codec)
}

func bitmapSubtitle(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "hdmv_pgs_subtitle", "pgs", "dvd_subtitle", "dvdsub", "dvb_subtitle", "dvbsub", "xsub":
		return true
	default:
		return false
	}
}

func applySubtitleDecision(sources []Source, streamAssets []storedAsset, subtitles []Subtitle, subtitleAssets []storedAsset, capabilities Capabilities, allowTranscoding bool) error {
	selectedID := selectedSubtitle(subtitles)
	if selectedID == "" {
		setSubtitleAction(sources, streamAssets, "none")
		return nil
	}
	var selected *Subtitle
	for index := range subtitles {
		if subtitles[index].ID == selectedID {
			selected = &subtitles[index]
			break
		}
	}
	if selected == nil || selected.Delivery != "burn" {
		setSubtitleAction(sources, streamAssets, "external")
		return nil
	}
	if !allowTranscoding {
		return ErrTranscodingDisabled
	}
	if !requestedProcessingMode(capabilities.SubtitleModes, "burn") ||
		!requestedProcessingMode(capabilities.ProcessingModes, processingTranscode) || !processingOutputSupported(capabilities) {
		return ErrClientCapabilityMissing
	}
	subtitleAssetIndex := storedAssetIndex(subtitleAssets, selected.ID)
	if subtitleAssetIndex < 0 || subtitleAssets[subtitleAssetIndex].SubtitleTrackIndex == nil {
		return ErrUnsupportedSource
	}
	for sourceIndex := range sources {
		source := &sources[sourceIndex]
		if !source.Compatible || source.Media == nil {
			continue
		}
		assetIndex := storedAssetIndex(streamAssets, source.ID)
		if assetIndex < 0 {
			continue
		}
		audio := selectedInspectionAudioTrack(*source.Media, streamAssets[assetIndex].AudioTrackIndex)
		decisionInspection := inspectionWithSelectedAudio(*source.Media, audio)
		audioAction := "transcode"
		targetAudio := &MediaTrack{Codec: "aac", Channels: targetAudioChannels(capabilities)}
		targetCodec, targetBitDepth := "", 0
		if audio == nil {
			audioAction = "copy"
			targetAudio = nil
		} else if subtitleBurnAudioCopySupported(audio, capabilities) {
			if codec, bitDepth := selectedTranscodeVideoTarget(capabilities, decisionInspection, audio); codec != "" {
				audioAction = "copy"
				targetAudio = audio
				targetCodec, targetBitDepth = codec, bitDepth
			}
		}
		if targetCodec == "" {
			targetCodec, targetBitDepth = selectedTranscodeVideoTarget(capabilities, decisionInspection, targetAudio)
		}
		if targetCodec == "" {
			return ErrClientCapabilityMissing
		}
		// Subtitle composition runs on CPU frames and has no probed Main 10 filter path.
		targetBitDepth = 8
		toneMap := videoTranscodeNeedsToneMapping(decisionInspection, capabilities, targetAudio)
		hdrFormat := strings.TrimSpace(decisionInspection.HDRFormat)
		if hdrFormat != "" && !strings.EqualFold(hdrFormat, "sdr") {
			toneMap = true
		}
		if toneMap && !genericToneMappingSupported(decisionInspection) {
			return ErrUnsupportedSource
		}
		reasons := directIncompatibilityReasons(*source, decisionInspection, capabilities)
		decision := processingDecisionWithReasons(decisionSubtitleBurnRequired, reasons, "transcode", audioAction, decisionInspection, capabilities, toneMap)
		if decision.Target == nil {
			return ErrClientCapabilityMissing
		}
		decision.Target.VideoCodec = targetCodec
		decision.Target.VideoBitDepth = targetBitDepth
		if source.Decision != nil {
			for _, reason := range source.Decision.Reasons {
				if reason != reasonAudioCodecNotSupported {
					appendDecisionReason(decision, reason)
				}
			}
		}
		appendDecisionReason(decision, decisionSubtitleBurnRequired)
		decision.SubtitleAction = "burn"
		decision.Pipeline = plannedPlaybackPipeline(decisionInspection, capabilities, decision.Target, toneMap, true)
		if audioAction == "copy" && audio != nil {
			decision.Target.AudioCodec = normalizedCodec(audio.Codec)
		}
		candidate := sourceCandidate{sourceIndex: sourceIndex, assetIndex: assetIndex}
		applyPlaybackDecision(sources, streamAssets, candidate, *source.Media, processingTranscode, decision, capabilities)
		index := *subtitleAssets[subtitleAssetIndex].SubtitleTrackIndex
		streamAssets[assetIndex].SubtitleTrackIndex = &index
		streamAssets[assetIndex].SubtitleTrackType = subtitleAssets[subtitleAssetIndex].SubtitleTrackType
		streamAssets[assetIndex].SubtitleTrackOrdinal = subtitleAssets[subtitleAssetIndex].SubtitleTrackOrdinal
		return nil
	}
	return ErrNoPlayableSource
}

func selectedInspectionAudioTrack(inspection MediaInspection, selectedIndex *int) *MediaTrack {
	if selectedIndex != nil {
		for index := range inspection.AudioTracks {
			if inspection.AudioTracks[index].Index == *selectedIndex {
				return &inspection.AudioTracks[index]
			}
		}
	}
	return primaryTrack(inspection.AudioTracks)
}

func inspectionWithSelectedAudio(inspection MediaInspection, audio *MediaTrack) MediaInspection {
	selected := cloneMediaInspection(inspection)
	if audio == nil {
		selected.AudioTracks = nil
	} else {
		selected.AudioTracks = []MediaTrack{*audio}
	}
	return selected
}

func subtitleBurnAudioCopySupported(audio *MediaTrack, capabilities Capabilities) bool {
	if audio == nil || !hlsSegmentAudioCopySupported(audio.Codec, capabilities.HLSSegmentContainer) ||
		capabilities.MaximumAudioChannels > 0 && (audio.Channels <= 0 || audio.Channels > capabilities.MaximumAudioChannels) {
		return false
	}
	return len(capabilities.MediaProfiles) > 0 || supportsCodec(capabilities.AudioCodecs, audio.Codec)
}

func hlsSegmentAudioCopySupported(codec, segmentContainer string) bool {
	if normalizedHLSSegmentContainer(segmentContainer) == "mp4" {
		return mp4RemuxableAudio(codec)
	}
	switch normalizedCodec(codec) {
	case "aac", "ac3", "eac3", "mp3":
		return true
	default:
		return false
	}
}

// Generic FFmpeg tone mapping can consume only recognized HDR10, HLG, or SDR
// Dolby Vision base layers. Profile 5 and unknown compatibility IDs fail closed.
func genericToneMappingSupported(inspection MediaInspection) bool {
	if !strings.EqualFold(strings.TrimSpace(inspection.HDRFormat), "dolby_vision") {
		return true
	}
	video := primaryTrack(inspection.VideoTracks)
	return video != nil && dolbyVisionBaseHDRFormat(video.DolbyVisionProfile, video.DolbyVisionBLPresent, video.DolbyVisionCompatibilityID) != ""
}

func setSubtitleAction(sources []Source, assets []storedAsset, action string) {
	for index := range sources {
		if !sources[index].Compatible {
			continue
		}
		if sources[index].Decision == nil {
			sources[index].Decision = directDecision(MediaInspection{})
		}
		sources[index].Decision.SubtitleAction = action
		if assetIndex := storedAssetIndex(assets, sources[index].ID); assetIndex >= 0 {
			assets[assetIndex].Decision = clonePlaybackDecision(sources[index].Decision)
		}
		return
	}
}
func applySubtitlePreference(subtitles []Subtitle, preferredID, forcedLanguage, preferredLanguage string) error {
	clearSubtitleDefaults(subtitles)
	if preferredID != "" && preferredID != "none" {
		for index := range subtitles {
			if subtitles[index].ID == preferredID {
				subtitles[index].Default = true
				return nil
			}
		}
		return ErrInvalidInput
	}
	if preferredID == "none" {
		return nil
	}
	if forcedLanguage != "" && forcedLanguage != "off" {
		for index := range subtitles {
			if subtitles[index].Forced && languageMatches(subtitles[index].Language, forcedLanguage) {
				subtitles[index].Default = true
				return nil
			}
		}
	}
	for index := range subtitles {
		if languageMatches(subtitles[index].Language, preferredLanguage) {
			subtitles[index].Default = true
			return nil
		}
	}
	return nil
}

func clearSubtitleDefaults(subtitles []Subtitle) {
	for index := range subtitles {
		subtitles[index].Default = false
	}
}

func selectedAudioTrack(sources []Source, assets []storedAsset) *int {
	for _, source := range sources {
		if !source.Compatible {
			continue
		}
		if assetIndex := storedAssetIndex(assets, source.ID); assetIndex >= 0 {
			return assets[assetIndex].AudioTrackIndex
		}
	}
	return nil
}

func selectedSubtitle(subtitles []Subtitle) string {
	for _, subtitle := range subtitles {
		if subtitle.Default {
			return subtitle.ID
		}
	}
	return ""
}

func languageMatches(candidate, preferred string) bool {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if candidate == "" || preferred == "" || preferred == "auto" {
		return false
	}
	if candidate == preferred {
		return true
	}
	candidateBase, _, _ := strings.Cut(candidate, "-")
	preferredBase, _, _ := strings.Cut(preferred, "-")
	return candidateBase == preferredBase
}
