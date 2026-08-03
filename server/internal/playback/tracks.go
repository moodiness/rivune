package playback

import (
	"strconv"
	"strings"
)

const (
	assetKindEmbeddedSubtitle  = "embedded_subtitle"
	assetKindConvertedSubtitle = "converted_subtitle"
	assetKindBitmapSubtitle    = "bitmap_subtitle"
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
		track, explicit := preferredAudioTrack(source.Media.AudioTracks, input.PreferredAudioTrack, input.PreferredAudioLanguage)
		if explicit && track == nil {
			return ErrInvalidInput
		}
		if track == nil {
			return nil
		}
		primary := primaryTrack(source.Media.AudioTracks)
		video := primaryTrack(source.Media.VideoTracks)
		trackCopySupported := video != nil && mp4RemuxableAudio(track.Codec) &&
			audioWithinClientLimits(track, input.Capabilities) &&
			mediaProfileSupported("mp4", video, track, input.Capabilities)
		requiresTrackSwitch := primary != nil && primary.Index != track.Index

		if source.Mode == processingRemux && !trackCopySupported && !explicit && video != nil {
			if compatible := compatibleRemuxAudioTrack(*video, source.Media.AudioTracks, input.Capabilities); compatible != nil {
				track = compatible
				trackCopySupported = true
			}
		}
		if source.Mode == "direct" && requiresTrackSwitch && trackCopySupported &&
			requestedProcessingMode(input.Capabilities.ProcessingModes, processingRemux) && processingOutputSupported(input.Capabilities) {
			decision := processingDecision(decisionRemuxRequired, "copy", "copy", *source.Media, input.Capabilities, false)
			applyPlaybackDecision(sources, assets, sourceCandidate{sourceIndex: sourceIndex, assetIndex: assetIndex}, *source.Media, processingRemux, decision, input.Capabilities)
			source = &sources[sourceIndex]
		} else if (source.Mode == "direct" && requiresTrackSwitch || source.Mode == processingRemux) && !trackCopySupported {
			mode := ""
			var decision *PlaybackDecision
			switch {
			case audioTranscodeSupported(*source.Media, input.Capabilities):
				mode = processingTranscodeAudio
				decision = processingDecision(decisionAudioTranscodeRequired, "copy", "transcode", *source.Media, input.Capabilities, false)
			case fullTranscodeSupported(input.Capabilities):
				mode = processingTranscode
				toneMap := !clientSupportsHDR(source.Media.HDRFormat, input.Capabilities)
				decision = processingDecision(decisionVideoTranscodeRequired, "transcode", "transcode", *source.Media, input.Capabilities, toneMap)
			default:
				if !input.AllowTranscoding {
					return ErrTranscodingDisabled
				}
				return ErrClientCapabilityMissing
			}
			if !input.AllowTranscoding {
				return ErrTranscodingDisabled
			}
			applyPlaybackDecision(sources, assets, sourceCandidate{sourceIndex: sourceIndex, assetIndex: assetIndex}, *source.Media, mode, decision, input.Capabilities)
		} else if source.Mode == "direct" && requiresTrackSwitch {
			return ErrClientCapabilityMissing
		}
		index := track.Index
		assets[assetIndex].AudioTrackIndex = &index
		return nil
	}
	return nil
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
		for _, track := range source.Media.SubtitleTracks {
			kind := assetKindEmbeddedSubtitle
			delivery := "external"
			switch {
			case webVTTConvertibleSubtitle(track.Codec) &&
				(len(capabilities.SubtitleModes) == 0 || requestedProcessingMode(capabilities.SubtitleModes, "external")):
			case bitmapSubtitle(track.Codec) && requestedProcessingMode(capabilities.SubtitleModes, "burn"):
				kind = assetKindBitmapSubtitle
				delivery = "burn"
			default:
				continue
			}
			trackIndex := track.Index
			id := "embedded-subtitle-" + strconv.Itoa(track.Index)
			subtitles = append(subtitles, Subtitle{
				ID: id, AddonID: source.AddonID, ManifestID: source.ManifestID,
				Language: track.Language, Forced: track.Forced, Delivery: delivery,
			})
			subtitleAssets = append(subtitleAssets, storedAsset{
				ID: id, Kind: kind, URL: sourceAsset.URL, Container: sourceAsset.Container,
				Headers: sourceAsset.Headers, SubtitleTrackIndex: &trackIndex,
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

func bitmapSubtitle(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "hdmv_pgs_subtitle", "pgs", "dvd_subtitle", "dvdsub":
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
	if !requestedProcessingMode(capabilities.SubtitleModes, "burn") || !fullTranscodeSupported(capabilities) {
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
		toneMap := !clientSupportsHDR(source.Media.HDRFormat, capabilities)
		decision := processingDecision(decisionSubtitleBurnRequired, "transcode", "transcode", *source.Media, capabilities, toneMap)
		decision.SubtitleAction = "burn"
		candidate := sourceCandidate{sourceIndex: sourceIndex, assetIndex: assetIndex}
		applyPlaybackDecision(sources, streamAssets, candidate, *source.Media, processingTranscode, decision, capabilities)
		index := *subtitleAssets[subtitleAssetIndex].SubtitleTrackIndex
		streamAssets[assetIndex].SubtitleTrackIndex = &index
		return nil
	}
	return ErrNoPlayableSource
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
