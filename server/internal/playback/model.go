package playback

import (
	"errors"
	"time"
)

var (
	ErrActiveProfileRequired   = errors.New("an active profile is required")
	ErrInvalidInput            = errors.New("invalid playback request")
	ErrForbidden               = errors.New("playback administration forbidden")
	ErrNoPlayableSource        = errors.New("no compatible playback source")
	ErrUnsupportedSource       = errors.New("source requires unsupported media conversion")
	ErrTranscodingDisabled     = errors.New("playback transcoding is disabled")
	ErrClientCapabilityMissing = errors.New("playback client capability is missing")
	ErrSessionNotFound         = errors.New("playback session not found")
	ErrProviderUnavailable     = errors.New("playback provider unavailable")
	ErrSourceReferenceExpired  = errors.New("playback source reference is invalid or expired")
	ErrMediaSourceFailed       = errors.New("media source failed")
	ErrMediaCapacityReached    = errors.New("media processing capacity reached")
	ErrMediaStorageLimit       = errors.New("media storage limit reached")
)

const sessionTTL = 2 * time.Hour

type MediaProfile struct {
	Container  string `json:"container"`
	VideoCodec string `json:"videoCodec"`
	AudioCodec string `json:"audioCodec,omitempty"`
}

type Capabilities struct {
	StreamingProtocols        []string       `json:"streamingProtocols,omitempty"`
	Containers                []string       `json:"containers,omitempty"`
	VideoCodecs               []string       `json:"videoCodecs,omitempty"`
	AudioCodecs               []string       `json:"audioCodecs,omitempty"`
	HDRFormats                []string       `json:"hdrFormats,omitempty"`
	ExternalPlayers           []string       `json:"externalPlayers,omitempty"`
	ProcessingModes           []string       `json:"processingModes,omitempty"`
	MediaProfiles             []MediaProfile `json:"mediaProfiles,omitempty"`
	MaximumVideoBitrateKbps   int            `json:"maximumVideoBitrateKbps,omitempty"`
	MaximumAudioChannels      int            `json:"maximumAudioChannels,omitempty"`
	SubtitleModes             []string       `json:"subtitleModes,omitempty"`
	MaximumHeight             int            `json:"maximumHeight,omitempty"`
	PreferDirectPlay          *bool          `json:"-"`
	TranscodeVideoBitrateKbps int            `json:"-"`
}

type SourcesInput struct {
	MediaType                       string       `json:"mediaType"`
	AddonID                         string       `json:"addonId,omitempty"`
	ResourceID                      string       `json:"resourceId"`
	Capabilities                    Capabilities `json:"capabilities"`
	MaximumSources                  int          `json:"-"`
	PreferredAudioLanguage          string       `json:"-"`
	PreferredSubtitleLanguage       string       `json:"-"`
	PreferredForcedSubtitleLanguage string       `json:"-"`
}

type SourceOption struct {
	ID             string    `json:"id"`
	SourceRef      string    `json:"sourceRef"`
	AddonID        string    `json:"addonId"`
	ManifestID     string    `json:"manifestId"`
	AddonName      string    `json:"addonName,omitempty"`
	StreamIndex    int       `json:"streamIndex"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
	Filename       string    `json:"filename,omitempty"`
	Protocol       string    `json:"protocol"`
	Container      string    `json:"container,omitempty"`
	ExpiresAt      time.Time `json:"expiresAt"`
	StableIdentity string    `json:"-"`
}

func (SourceOption) String() string {
	return "playback.SourceOption(redacted)"
}

type SourceList struct {
	Sources        []SourceOption    `json:"sources"`
	ProviderErrors []ProviderFailure `json:"providerErrors"`
}

type PrepareInput struct {
	SourceRef        string  `json:"sourceRef"`
	StartSeconds     float64 `json:"startSeconds,omitempty"`
	AllowTranscoding bool    `json:"-"`
	MaximumHeight    int     `json:"-"`
}

type Preparation struct {
	SourceRef     string            `json:"sourceRef"`
	Mode          string            `json:"mode"`
	Protocol      string            `json:"protocol"`
	Container     string            `json:"container,omitempty"`
	Media         *MediaInspection  `json:"media,omitempty"`
	Decision      *PlaybackDecision `json:"decision,omitempty"`
	SubtitleCount int               `json:"subtitleCount"`
	ExpiresAt     time.Time         `json:"expiresAt"`
}

type ResolveInput struct {
	SourceRef                       string       `json:"sourceRef"`
	TitleID                         string       `json:"titleId,omitempty"`
	PreferredAudioTrack             *int         `json:"preferredAudioTrack,omitempty"`
	PreferredSubtitleID             string       `json:"preferredSubtitleId,omitempty"`
	StartSeconds                    float64      `json:"startSeconds,omitempty"`
	PreferredAudioLanguage          string       `json:"-"`
	PreferredSubtitleLanguage       string       `json:"-"`
	PreferredForcedSubtitleLanguage string       `json:"-"`
	Capabilities                    Capabilities `json:"-"`
	AllowTranscoding                bool         `json:"-"`
	MaximumHeight                   int          `json:"-"`
}

type MediaTrack struct {
	Index       int    `json:"index"`
	Type        string `json:"type"`
	Codec       string `json:"codec"`
	Profile     string `json:"profile,omitempty"`
	Language    string `json:"language,omitempty"`
	Title       string `json:"title,omitempty"`
	Forced      bool   `json:"forced,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Channels    int    `json:"channels,omitempty"`
	BitrateKbps int    `json:"-"`
}

type MediaInspection struct {
	Container       string       `json:"container,omitempty"`
	DurationSeconds float64      `json:"durationSeconds,omitempty"`
	HDRFormat       string       `json:"hdrFormat,omitempty"`
	VideoTracks     []MediaTrack `json:"videoTracks"`
	AudioTracks     []MediaTrack `json:"audioTracks"`
	SubtitleTracks  []MediaTrack `json:"subtitleTracks"`
}

type Source struct {
	ID          string            `json:"id"`
	AddonID     string            `json:"addonId"`
	ManifestID  string            `json:"manifestId"`
	AddonName   string            `json:"-"`
	Name        string            `json:"name,omitempty"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"-"`
	Hint        string            `json:"-"`
	Filename    string            `json:"-"`
	StreamIndex int               `json:"-"`
	Mode        string            `json:"mode"`
	URL         string            `json:"url,omitempty"`
	YTID        string            `json:"ytId,omitempty"`
	InfoHash    string            `json:"infoHash,omitempty"`
	FileIndex   *int              `json:"fileIndex,omitempty"`
	Protocol    string            `json:"protocol"`
	Container   string            `json:"container,omitempty"`
	Compatible  bool              `json:"compatible"`
	Media       *MediaInspection  `json:"media,omitempty"`
	Decision    *PlaybackDecision `json:"decision,omitempty"`
}

type Subtitle struct {
	ID         string `json:"id"`
	AddonID    string `json:"addonId"`
	ManifestID string `json:"manifestId"`
	Language   string `json:"language,omitempty"`
	URL        string `json:"url,omitempty"`
	Delivery   string `json:"delivery"`
	Forced     bool   `json:"forced,omitempty"`
	Default    bool   `json:"default,omitempty"`
}

type ProviderFailure struct {
	AddonID    string `json:"addonId"`
	ManifestID string `json:"manifestId"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

type Session struct {
	ID                 string            `json:"id"`
	SelectedSourceID   string            `json:"selectedSourceId"`
	SelectedAudioTrack *int              `json:"selectedAudioTrack,omitempty"`
	SelectedSubtitleID string            `json:"selectedSubtitleId,omitempty"`
	Sources            []Source          `json:"sources"`
	Subtitles          []Subtitle        `json:"subtitles"`
	ProviderErrors     []ProviderFailure `json:"providerErrors"`
	ExpiresAt          time.Time         `json:"expiresAt"`
}

type PlaybackDecision struct {
	Reason         string                  `json:"reason"`
	VideoAction    string                  `json:"videoAction"`
	AudioAction    string                  `json:"audioAction"`
	SubtitleAction string                  `json:"subtitleAction"`
	ToneMapping    bool                    `json:"toneMapping"`
	Source         *PlaybackDecisionSource `json:"source,omitempty"`
	Target         *PlaybackDecisionTarget `json:"target,omitempty"`
}

type PlaybackDecisionSource struct {
	Container        string `json:"container,omitempty"`
	VideoCodec       string `json:"videoCodec,omitempty"`
	AudioCodec       string `json:"audioCodec,omitempty"`
	Height           int    `json:"height,omitempty"`
	VideoBitrateKbps int    `json:"videoBitrateKbps,omitempty"`
	HDRFormat        string `json:"hdrFormat,omitempty"`
}

type PlaybackDecisionTarget struct {
	Protocol         string `json:"protocol,omitempty"`
	Container        string `json:"container,omitempty"`
	VideoCodec       string `json:"videoCodec,omitempty"`
	AudioCodec       string `json:"audioCodec,omitempty"`
	Height           int    `json:"height,omitempty"`
	VideoBitrateKbps int    `json:"videoBitrateKbps,omitempty"`
}

type storedAsset struct {
	ID                   string            `json:"id"`
	Kind                 string            `json:"kind"`
	URL                  string            `json:"url"`
	Container            string            `json:"container,omitempty"`
	Headers              map[string]string `json:"headers,omitempty"`
	ToneMap              bool              `json:"toneMap,omitempty"`
	AudioTrackIndex      *int              `json:"audioTrackIndex,omitempty"`
	SubtitleTrackIndex   *int              `json:"subtitleTrackIndex,omitempty"`
	DurationSeconds      float64           `json:"durationSeconds,omitempty"`
	Decision             *PlaybackDecision `json:"decision,omitempty"`
	TargetHeight         int               `json:"targetHeight,omitempty"`
	VideoBitrateKbps     int               `json:"videoBitrateKbps,omitempty"`
	MaximumAudioChannels int               `json:"maximumAudioChannels,omitempty"`
	StartSeconds         float64           `json:"-"`
	ReadRate             float64           `json:"-"`
}
