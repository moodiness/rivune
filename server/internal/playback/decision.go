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

func (service *Service) decidePlaybackSource(ctx context.Context, sources []Source, assets []storedAsset, capabilities Capabilities) {
	if service.processor == nil {
		return
	}
	for index := range sources {
		source := &sources[index]
		if !source.Compatible || (source.Mode != "youtube" && source.Protocol != "hls") {
			continue
		}
		if capabilities.MaximumHeight > 0 && sourceResolutionHint(*source) > capabilities.MaximumHeight {
			source.Compatible = false
			continue
		}
		if source.Mode == "youtube" {
			return
		}
		if assetIndex := storedAssetIndex(assets, source.ID); assetIndex >= 0 {
			if inspection, err := service.probeMedia(ctx, assets[assetIndex]); err == nil {
				source.Media = &inspection
				if inspection.Container != "" {
					source.Container = inspection.Container
				}
			}
		}
		return
	}

	candidates := make([]sourceCandidate, 0)
	for sourceIndex := range sources {
		source := &sources[sourceIndex]
		if source.Mode != "direct" || source.URL == "" || source.Protocol != "http" {
			continue
		}
		assetIndex := storedAssetIndex(assets, source.ID)
		if assetIndex < 0 {
			continue
		}
		resolutionHint := sourceResolutionHint(*source)
		if capabilities.MaximumHeight > 0 && resolutionHint > capabilities.MaximumHeight {
			source.Compatible = false
			continue
		}
		candidates = append(candidates, sourceCandidate{
			sourceIndex:    sourceIndex,
			assetIndex:     assetIndex,
			directHint:     source.Compatible,
			resolutionHint: resolutionHint,
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
		leftHint := sources[candidates[left].sourceIndex].Hint
		rightHint := sources[candidates[right].sourceIndex].Hint
		return remuxHintRank(leftHint) < remuxHintRank(rightHint)
	})

	fallbackIndex := -1
	fallbackHeight := 0
	var fallbackInspection MediaInspection
	var fallbackMode string
	var fallbackToneMap bool
	for index, candidate := range candidates {
		inspection, err := service.probeMedia(ctx, assets[candidate.assetIndex])
		if err != nil {
			continue
		}
		source := &sources[candidate.sourceIndex]
		if inspection.Container != "" {
			source.Container = inspection.Container
		}
		mode, toneMap := playbackMode(*source, inspection, capabilities)
		if mode == "" {
			continue
		}
		video := primaryTrack(inspection.VideoTracks)
		if video == nil {
			continue
		}
		if capabilities.MaximumHeight > 0 && video.Height > capabilities.MaximumHeight {
			continue
		}
		if candidate.resolutionHint == 0 || video.Height >= candidate.resolutionHint*9/10 {
			applyPlaybackDecision(sources, assets, candidate, inspection, mode, toneMap, capabilities)
			return
		}
		if video.Height > fallbackHeight {
			fallbackIndex = index
			fallbackHeight = video.Height
			fallbackInspection = inspection
			fallbackMode = mode
			fallbackToneMap = toneMap
		}
	}
	if fallbackIndex >= 0 {
		applyPlaybackDecision(sources, assets, candidates[fallbackIndex], fallbackInspection, fallbackMode, fallbackToneMap, capabilities)
	}
}

func applyPlaybackDecision(sources []Source, assets []storedAsset, candidate sourceCandidate, inspection MediaInspection, mode string, toneMap bool, capabilities Capabilities) {
	source := &sources[candidate.sourceIndex]
	source.Media = &inspection
	source.Mode = mode
	source.Compatible = true
	if mode == "direct" {
		return
	}
	if supports(capabilities.StreamingProtocols, "hls") {
		source.Protocol = "hls"
		source.Container = "hls"
	} else {
		source.Protocol = "http"
		source.Container = "mp4"
	}
	assets[candidate.assetIndex].Kind = mode
	assets[candidate.assetIndex].ToneMap = toneMap
}

func playbackMode(source Source, inspection MediaInspection, capabilities Capabilities) (string, bool) {
	video := primaryTrack(inspection.VideoTracks)
	if video == nil {
		return "", false
	}
	audio := primaryTrack(inspection.AudioTracks)
	videoSupported := supportsCodec(capabilities.VideoCodecs, video.Codec)
	audioSupported := audio == nil || supportsCodec(capabilities.AudioCodecs, audio.Codec)
	hdrSupported := inspection.HDRFormat == "" || supports(capabilities.HDRFormats, inspection.HDRFormat)
	containerSupported := supportsContainer(capabilities.Containers, source.Container)
	protocolSupported := supports(capabilities.StreamingProtocols, source.Protocol)
	preferDirect := capabilities.PreferDirectPlay == nil || *capabilities.PreferDirectPlay
	if preferDirect && protocolSupported && containerSupported && videoSupported && audioSupported && hdrSupported {
		return "direct", false
	}
	canHLS := supports(capabilities.StreamingProtocols, "hls")
	canProgressiveMP4 := supports(capabilities.StreamingProtocols, "http") && supportsContainer(capabilities.Containers, "mp4")
	if !canHLS && !canProgressiveMP4 {
		return "", false
	}
	if videoSupported && hdrSupported && mp4RemuxableVideo(video.Codec) {
		if audioSupported && (audio == nil || mp4RemuxableAudio(audio.Codec)) {
			return processingRemux, false
		}
		if supportsCodec(capabilities.AudioCodecs, "aac") {
			return processingTranscodeAudio, false
		}
	}
	if !supportsCodec(capabilities.VideoCodecs, "h264") || !supportsCodec(capabilities.AudioCodecs, "aac") {
		return "", false
	}
	return processingTranscode, inspection.HDRFormat != "" && !hdrSupported
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
	case "aac", "ac3", "eac3", "mp3", "alac":
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
