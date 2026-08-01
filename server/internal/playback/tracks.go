package playback

import (
	"strconv"
	"strings"
)

const (
	assetKindEmbeddedSubtitle  = "embedded_subtitle"
	assetKindConvertedSubtitle = "converted_subtitle"
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
		asset := &assets[assetIndex]
		track, explicit := preferredAudioTrack(source.Media.AudioTracks, input.PreferredAudioTrack, input.PreferredAudioLanguage)
		if explicit && track == nil {
			return ErrInvalidInput
		}
		if track == nil {
			return nil
		}
		primary := primaryTrack(source.Media.AudioTracks)
		video := primaryTrack(source.Media.VideoTracks)
		requiresRemux := source.Mode == processingRemux || source.Mode == "direct" && primary != nil && primary.Index != track.Index
		if requiresRemux && (video == nil || !mp4RemuxableAudio(track.Codec) || !mediaProfileSupported("mp4", video, track, input.Capabilities)) {
			if explicit || video == nil {
				return ErrUnsupportedSource
			}
			track = compatibleRemuxAudioTrack(*video, source.Media.AudioTracks, input.Capabilities)
			if track == nil {
				return ErrUnsupportedSource
			}
		}
		index := track.Index
		asset.AudioTrackIndex = &index
		if source.Mode == "direct" && primary != nil && primary.Index != track.Index {
			if !requestedProcessingMode(input.Capabilities.ProcessingModes, processingRemux) {
				return ErrUnsupportedSource
			}
			source.Mode = processingRemux
			if supports(input.Capabilities.StreamingProtocols, "hls") {
				source.Protocol = "hls"
				source.Container = "hls"
			} else {
				source.Protocol = "http"
				source.Container = "mp4"
			}
			asset.Kind = processingRemux
		}
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

func embeddedSubtitles(sources []Source, assets []storedAsset) ([]Subtitle, []storedAsset) {
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
			if !webVTTConvertibleSubtitle(track.Codec) {
				continue
			}
			trackIndex := track.Index
			id := "embedded-subtitle-" + strconv.Itoa(track.Index)
			subtitles = append(subtitles, Subtitle{ID: id, AddonID: source.AddonID, ManifestID: source.ManifestID, Language: track.Language, Forced: track.Forced})
			subtitleAssets = append(subtitleAssets, storedAsset{
				ID: id, Kind: assetKindEmbeddedSubtitle, URL: sourceAsset.URL, Container: sourceAsset.Container,
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
