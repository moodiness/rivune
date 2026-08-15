package jellyfin

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

const (
	CompatibilityVersion     = "10.11.11"
	CompatibilityProduct     = "Rivune Jellyfin Compatibility"
	compatibilityProductName = "Jellyfin Server"
)

type PublicSystemInfo struct {
	Id                     string `json:"Id"`
	LocalAddress           string `json:"LocalAddress"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
	OperatingSystem        string `json:"OperatingSystem"`
}

type SystemInfo struct {
	PublicSystemInfo
}

type SystemEndpointInfo struct {
	IsLocal     bool `json:"IsLocal"`
	IsInNetwork bool `json:"IsInNetwork"`
}

type AuthenticateByName struct {
	Username string `json:"Username,omitempty"`
	UserName string `json:"UserName,omitempty"`
	Pw       string `json:"Pw,omitempty"`
	Password string `json:"Password,omitempty"`
}

type AuthenticationResult struct {
	User        UserDto        `json:"User"`
	SessionInfo SessionInfoDto `json:"SessionInfo"`
	AccessToken string         `json:"AccessToken"`
	ServerId    string         `json:"ServerId"`
}

const (
	quickConnectSecretPrefix    = "rivune_dc_"
	quickConnectSecretMaxLength = 128
)

func validQuickConnectSecret(secret string) bool {
	if len(secret) <= len(quickConnectSecretPrefix) || len(secret) > quickConnectSecretMaxLength ||
		secret[:len(quickConnectSecretPrefix)] != quickConnectSecretPrefix {
		return false
	}
	for index := len(quickConnectSecretPrefix); index < len(secret); index++ {
		current := secret[index]
		if current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' ||
			current >= '0' && current <= '9' || current == '_' || current == '-' {
			continue
		}
		return false
	}
	return true
}

type QuickConnectResult struct {
	Authenticated bool      `json:"Authenticated"`
	Secret        string    `json:"Secret"`
	Code          string    `json:"Code"`
	DateAdded     time.Time `json:"DateAdded"`
	DeviceID      string    `json:"DeviceId"`
	DeviceName    string    `json:"DeviceName"`
	AppName       string    `json:"AppName"`
	AppVersion    string    `json:"AppVersion"`
}

type AuthenticateWithQuickConnect struct {
	Secret string `json:"Secret"`
}

type UserDto struct {
	Name                      string            `json:"Name"`
	ServerId                  string            `json:"ServerId"`
	Id                        string            `json:"Id"`
	HasPassword               bool              `json:"HasPassword"`
	HasConfiguredPassword     bool              `json:"HasConfiguredPassword"`
	HasConfiguredEasyPassword bool              `json:"HasConfiguredEasyPassword"`
	Policy                    UserPolicy        `json:"Policy"`
	Configuration             UserConfiguration `json:"Configuration"`
}

type UserPolicy struct {
	IsAdministrator                 bool     `json:"IsAdministrator"`
	IsHidden                        bool     `json:"IsHidden"`
	IsDisabled                      bool     `json:"IsDisabled"`
	EnableUserPreferenceAccess      bool     `json:"EnableUserPreferenceAccess"`
	EnablePlayback                  bool     `json:"EnablePlayback"`
	EnableMediaPlayback             bool     `json:"EnableMediaPlayback"`
	EnableAudioPlaybackTranscoding  bool     `json:"EnableAudioPlaybackTranscoding"`
	EnableVideoPlaybackTranscoding  bool     `json:"EnableVideoPlaybackTranscoding"`
	EnablePlaybackRemuxing          bool     `json:"EnablePlaybackRemuxing"`
	ForceRemoteSourceTranscoding    bool     `json:"ForceRemoteSourceTranscoding"`
	EnableContentDownloading        bool     `json:"EnableContentDownloading"`
	EnableSyncTranscoding           bool     `json:"EnableSyncTranscoding"`
	EnableMediaConversion           bool     `json:"EnableMediaConversion"`
	EnableRemoteAccess              bool     `json:"EnableRemoteAccess"`
	EnableRemoteControlOfOtherUsers bool     `json:"EnableRemoteControlOfOtherUsers"`
	EnableSharedDeviceControl       bool     `json:"EnableSharedDeviceControl"`
	EnableAllDevices                bool     `json:"EnableAllDevices"`
	EnableAllChannels               bool     `json:"EnableAllChannels"`
	EnableAllFolders                bool     `json:"EnableAllFolders"`
	EnablePublicSharing             bool     `json:"EnablePublicSharing"`
	EnabledDevices                  []string `json:"EnabledDevices"`
	EnabledChannels                 []string `json:"EnabledChannels"`
	EnabledFolders                  []string `json:"EnabledFolders"`
	BlockedMediaFolders             []string `json:"BlockedMediaFolders"`
	BlockedChannels                 []string `json:"BlockedChannels"`
	MaxActiveSessions               int32    `json:"MaxActiveSessions"`
	RemoteClientBitrateLimit        int32    `json:"RemoteClientBitrateLimit"`
	AuthenticationProviderId        string   `json:"AuthenticationProviderId"`
	PasswordResetProviderId         string   `json:"PasswordResetProviderId"`
}

type UserConfiguration struct {
	AudioLanguagePreference    *string  `json:"AudioLanguagePreference"`
	PlayDefaultAudioTrack      bool     `json:"PlayDefaultAudioTrack"`
	SubtitleLanguagePreference *string  `json:"SubtitleLanguagePreference"`
	DisplayMissingEpisodes     bool     `json:"DisplayMissingEpisodes"`
	GroupedFolders             []string `json:"GroupedFolders"`
	SubtitleMode               string   `json:"SubtitleMode"`
	DisplayCollectionsView     bool     `json:"DisplayCollectionsView"`
	EnableLocalPassword        bool     `json:"EnableLocalPassword"`
	OrderedViews               []string `json:"OrderedViews"`
	LatestItemsExcludes        []string `json:"LatestItemsExcludes"`
	MyMediaExcludes            []string `json:"MyMediaExcludes"`
	HidePlayedInLatest         bool     `json:"HidePlayedInLatest"`
	RememberAudioSelections    bool     `json:"RememberAudioSelections"`
	RememberSubtitleSelections bool     `json:"RememberSubtitleSelections"`
	EnableNextEpisodeAutoPlay  bool     `json:"EnableNextEpisodeAutoPlay"`
	CastReceiverId             *string  `json:"CastReceiverId"`
}

type SessionInfoDto struct {
	Id                       string                `json:"Id"`
	ServerId                 string                `json:"ServerId"`
	IsActive                 bool                  `json:"IsActive"`
	UserId                   string                `json:"UserId"`
	UserName                 string                `json:"UserName"`
	Client                   string                `json:"Client"`
	DeviceName               string                `json:"DeviceName"`
	DeviceType               string                `json:"DeviceType"`
	DeviceId                 string                `json:"DeviceId"`
	ApplicationVersion       string                `json:"ApplicationVersion"`
	RemoteEndPoint           string                `json:"RemoteEndPoint"`
	LastActivityDate         time.Time             `json:"LastActivityDate"`
	PlayState                *PlayerStateInfo      `json:"PlayState,omitempty"`
	LastPlaybackCheckIn      *time.Time            `json:"LastPlaybackCheckIn,omitempty"`
	NowPlayingItem           *BaseItemDto          `json:"NowPlayingItem,omitempty"`
	NowViewingItem           *BaseItemDto          `json:"NowViewingItem,omitempty"`
	Capabilities             ClientCapabilitiesDto `json:"Capabilities"`
	PlayableMediaTypes       []string              `json:"PlayableMediaTypes"`
	SupportedCommands        []string              `json:"SupportedCommands"`
	SupportsMediaControl     bool                  `json:"SupportsMediaControl"`
	SupportsRemoteControl    bool                  `json:"SupportsRemoteControl"`
	HasCustomDeviceName      bool                  `json:"HasCustomDeviceName"`
	AdditionalUsers          []SessionUserInfoDto  `json:"AdditionalUsers"`
	NowPlayingQueue          []QueueItemDto        `json:"NowPlayingQueue"`
	NowPlayingQueueFullItems []BaseItemDto         `json:"NowPlayingQueueFullItems"`
}

type ClientCapabilitiesDto struct {
	PlayableMediaTypes           []string       `json:"PlayableMediaTypes"`
	SupportedCommands            []string       `json:"SupportedCommands"`
	SupportsMediaControl         bool           `json:"SupportsMediaControl"`
	SupportsPersistentIdentifier bool           `json:"SupportsPersistentIdentifier"`
	DeviceProfile                *DeviceProfile `json:"DeviceProfile,omitempty"`
}
type PlayerStateInfo struct {
	PositionTicks       *int64 `json:"PositionTicks,omitempty"`
	CanSeek             bool   `json:"CanSeek"`
	IsPaused            bool   `json:"IsPaused"`
	IsMuted             bool   `json:"IsMuted"`
	AudioStreamIndex    *int   `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex *int   `json:"SubtitleStreamIndex,omitempty"`
	MediaSourceId       string `json:"MediaSourceId,omitempty"`
	PlayMethod          string `json:"PlayMethod,omitempty"`
}

type UserDataChangeInfo struct {
	UserId       string            `json:"UserId"`
	UserDataList []UserItemDataDto `json:"UserDataList"`
}

type LibraryUpdateInfo struct {
	FoldersAddedTo     []string `json:"FoldersAddedTo"`
	FoldersRemovedFrom []string `json:"FoldersRemovedFrom"`
	ItemsAdded         []string `json:"ItemsAdded"`
	ItemsRemoved       []string `json:"ItemsRemoved"`
	ItemsUpdated       []string `json:"ItemsUpdated"`
	CollectionFolders  []string `json:"CollectionFolders"`
	IsEmpty            bool     `json:"IsEmpty"`
}

type SessionUserInfoDto struct {
	UserId   string `json:"UserId"`
	UserName string `json:"UserName"`
}

type QueueItemDto struct {
	Id             string `json:"Id"`
	PlaylistItemId string `json:"PlaylistItemId"`
}

type SpecialViewOptionDto struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

type WebSocketMessageDto struct {
	MessageType string `json:"MessageType"`
	MessageId   string `json:"MessageId,omitempty"`
	Data        any    `json:"Data,omitempty"`
}

type QueryResult[T any] struct {
	Items            []T `json:"Items"`
	TotalRecordCount int `json:"TotalRecordCount"`
	StartIndex       int `json:"StartIndex"`
}
type NameGuidPair struct {
	Name string `json:"Name"`
	Id   string `json:"Id"`
}

type QueryFiltersLegacy struct {
	Genres          []string `json:"Genres"`
	Tags            []string `json:"Tags"`
	OfficialRatings []string `json:"OfficialRatings"`
	Years           []int    `json:"Years"`
}

type QueryFilters struct {
	Genres []NameGuidPair `json:"Genres"`
	Tags   []string       `json:"Tags"`
}

type RecommendationDto struct {
	Items              []BaseItemDto `json:"Items"`
	RecommendationType string        `json:"RecommendationType"`
	BaselineItemName   string        `json:"BaselineItemName"`
	CategoryId         string        `json:"CategoryId"`
}

type MediaSegmentDto struct {
	Id         string `json:"Id"`
	ItemId     string `json:"ItemId"`
	Type       string `json:"Type"`
	StartTicks int64  `json:"StartTicks"`
	EndTicks   int64  `json:"EndTicks"`
}

type ThemeMediaResult struct {
	Items            []BaseItemDto `json:"Items"`
	TotalRecordCount int           `json:"TotalRecordCount"`
	StartIndex       int           `json:"StartIndex"`
	OwnerId          string        `json:"OwnerId"`
}

type AllThemeMediaResult struct {
	ThemeVideosResult     *ThemeMediaResult `json:"ThemeVideosResult"`
	ThemeSongsResult      *ThemeMediaResult `json:"ThemeSongsResult"`
	SoundtrackSongsResult *ThemeMediaResult `json:"SoundtrackSongsResult"`
}

type BaseItemDto struct {
	Id                      string                              `json:"Id"`
	ServerId                string                              `json:"ServerId"`
	Name                    string                              `json:"Name"`
	Etag                    string                              `json:"Etag,omitempty"`
	Path                    string                              `json:"Path,omitempty"`
	DisplayPreferencesId    string                              `json:"DisplayPreferencesId,omitempty"`
	LocationType            string                              `json:"LocationType,omitempty"`
	SortName                string                              `json:"SortName,omitempty"`
	OriginalTitle           string                              `json:"OriginalTitle,omitempty"`
	Type                    string                              `json:"Type"`
	MediaType               string                              `json:"MediaType,omitempty"`
	IsFolder                bool                                `json:"IsFolder"`
	IsPlayable              bool                                `json:"IsPlayable"`
	CanDownload             bool                                `json:"CanDownload"`
	CollectionType          string                              `json:"CollectionType,omitempty"`
	ParentId                string                              `json:"ParentId,omitempty"`
	SeriesId                string                              `json:"SeriesId,omitempty"`
	SeasonId                string                              `json:"SeasonId,omitempty"`
	SeriesName              string                              `json:"SeriesName,omitempty"`
	SeasonName              string                              `json:"SeasonName,omitempty"`
	IndexNumber             *int                                `json:"IndexNumber,omitempty"`
	ParentIndexNumber       *int                                `json:"ParentIndexNumber,omitempty"`
	Overview                string                              `json:"Overview,omitempty"`
	Taglines                []string                            `json:"Taglines,omitempty"`
	Status                  string                              `json:"Status,omitempty"`
	EndDate                 string                              `json:"EndDate,omitempty"`
	PremiereDate            string                              `json:"PremiereDate,omitempty"`
	ProductionYear          *int                                `json:"ProductionYear,omitempty"`
	DateCreated             string                              `json:"DateCreated,omitempty"`
	MediaSourceCount        *int                                `json:"MediaSourceCount,omitempty"`
	RunTimeTicks            *int64                              `json:"RunTimeTicks,omitempty"`
	Genres                  []string                            `json:"Genres,omitempty"`
	Studios                 []NameGuidPair                      `json:"Studios,omitempty"`
	CommunityRating         *float32                            `json:"CommunityRating,omitempty"`
	ProviderIds             map[string]string                   `json:"ProviderIds,omitempty"`
	PrimaryImageAspectRatio float64                             `json:"PrimaryImageAspectRatio,omitempty"`
	ImageTags               map[string]string                   `json:"ImageTags"`
	BackdropImageTags       []string                            `json:"BackdropImageTags"`
	UserData                *UserItemDataDto                    `json:"UserData,omitempty"`
	MediaSources            []MediaSourceInfo                   `json:"MediaSources,omitempty"`
	Trickplay               map[string]map[int]TrickplayInfoDto `json:"Trickplay,omitempty"`
	People                  []BaseItemPerson                    `json:"People,omitempty"`
	includeEmptyGenres      bool
}

// MarshalJSON preserves the established empty Genres array on virtual roots
// without defeating optional-field projection for ordinary catalog items.
func (item BaseItemDto) MarshalJSON() ([]byte, error) {
	type BaseItemDTOAlias BaseItemDto
	if !item.includeEmptyGenres || item.Genres == nil {
		return json.Marshal(BaseItemDTOAlias(item))
	}
	return json.Marshal(struct {
		BaseItemDTOAlias
		Genres []string `json:"Genres"`
	}{
		BaseItemDTOAlias: BaseItemDTOAlias(item),
		Genres:           item.Genres,
	})
}

type TrickplayInfoDto struct {
	Width          int `json:"Width"`
	Height         int `json:"Height"`
	TileWidth      int `json:"TileWidth"`
	TileHeight     int `json:"TileHeight"`
	ThumbnailCount int `json:"ThumbnailCount"`
	Interval       int `json:"Interval"`
	Bandwidth      int `json:"Bandwidth"`
}

type BaseItemPerson struct {
	Name            string `json:"Name"`
	Id              string `json:"Id"`
	Role            string `json:"Role,omitempty"`
	Type            string `json:"Type"`
	PrimaryImageTag string `json:"PrimaryImageTag,omitempty"`
}

type ImageInfo struct {
	ImageType  string `json:"ImageType"`
	ImageIndex int    `json:"ImageIndex"`
	ImageTag   string `json:"ImageTag"`
	Width      *int   `json:"Width,omitempty"`
	Height     *int   `json:"Height,omitempty"`
	Size       *int64 `json:"Size,omitempty"`
}

type UserItemDataDto struct {
	Rating                *float64 `json:"Rating,omitempty"`
	PlayedPercentage      *float64 `json:"PlayedPercentage,omitempty"`
	UnplayedItemCount     *int     `json:"UnplayedItemCount,omitempty"`
	PlaybackPositionTicks int64    `json:"PlaybackPositionTicks"`
	PlayCount             int      `json:"PlayCount"`
	IsFavorite            bool     `json:"IsFavorite"`
	Likes                 *bool    `json:"Likes,omitempty"`
	LastPlayedDate        string   `json:"LastPlayedDate,omitempty"`
	Played                bool     `json:"Played"`
	Key                   string   `json:"Key"`
	ItemId                string   `json:"ItemId"`
}

var errDuplicateUserDataField = errors.New("duplicate user data field")

type nullableUserDataUpdate[T any] struct {
	Set   bool
	Value *T
}

func (value *nullableUserDataUpdate[T]) UnmarshalJSON(data []byte) error {
	if value.Set {
		return errDuplicateUserDataField
	}
	value.Set = true
	data = bytes.TrimSpace(data)
	if !utf8.Valid(data) {
		return errors.New("user data field is not valid UTF-8")
	}
	if bytes.Equal(data, []byte("null")) {
		value.Value = nil
		return nil
	}
	var decoded T
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = &decoded
	return nil
}

type UpdateUserItemDataDto struct {
	Rating                nullableUserDataUpdate[float64] `json:"Rating"`
	PlayedPercentage      nullableUserDataUpdate[float64] `json:"PlayedPercentage"`
	UnplayedItemCount     nullableUserDataUpdate[int]     `json:"UnplayedItemCount"`
	PlaybackPositionTicks *int64                          `json:"PlaybackPositionTicks"`
	PlayCount             nullableUserDataUpdate[int]     `json:"PlayCount"`
	IsFavorite            *bool                           `json:"IsFavorite"`
	Likes                 nullableUserDataUpdate[bool]    `json:"Likes"`
	LastPlayedDate        nullableUserDataUpdate[string]  `json:"LastPlayedDate"`
	Played                *bool                           `json:"Played"`
	Key                   *string                         `json:"Key"`
	ItemId                *string                         `json:"ItemId"`
}

type SearchHintDto struct {
	ItemId                  string            `json:"ItemId"`
	Id                      string            `json:"Id"`
	Name                    string            `json:"Name"`
	Type                    string            `json:"Type"`
	MediaType               string            `json:"MediaType,omitempty"`
	PrimaryImageTag         string            `json:"PrimaryImageTag,omitempty"`
	ThumbImageTag           string            `json:"ThumbImageTag,omitempty"`
	BackdropImageTag        string            `json:"BackdropImageTag,omitempty"`
	Artists                 []string          `json:"Artists"`
	ChannelId               *string           `json:"ChannelId"`
	PrimaryImageAspectRatio float64           `json:"PrimaryImageAspectRatio,omitempty"`
	ProductionYear          *int              `json:"ProductionYear,omitempty"`
	RunTimeTicks            *int64            `json:"RunTimeTicks,omitempty"`
	ProviderIds             map[string]string `json:"ProviderIds,omitempty"`
}

type SearchHintResult struct {
	SearchHints      []SearchHintDto `json:"SearchHints"`
	TotalRecordCount int             `json:"TotalRecordCount"`
}

type PlaybackInfoRequest struct {
	UserId               string        `json:"UserId,omitempty"`
	StartTimeTicks       int64         `json:"StartTimeTicks,omitempty"`
	IsPlayback           bool          `json:"IsPlayback,omitempty"`
	AutoOpenLiveStream   bool          `json:"AutoOpenLiveStream,omitempty"`
	MaxStreamingBitrate  int64         `json:"MaxStreamingBitrate,omitempty"`
	MaxAudioChannels     int           `json:"MaxAudioChannels,omitempty"`
	MediaSourceId        string        `json:"MediaSourceId,omitempty"`
	AudioStreamIndex     *int          `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex  *int          `json:"SubtitleStreamIndex,omitempty"`
	EnableDirectPlay     *bool         `json:"EnableDirectPlay,omitempty"`
	EnableDirectStream   *bool         `json:"EnableDirectStream,omitempty"`
	EnableTranscoding    *bool         `json:"EnableTranscoding,omitempty"`
	AllowVideoStreamCopy *bool         `json:"AllowVideoStreamCopy,omitempty"`
	AllowAudioStreamCopy *bool         `json:"AllowAudioStreamCopy,omitempty"`
	DeviceProfile        DeviceProfile `json:"DeviceProfile"`
}

type PlaybackInfoResponse struct {
	MediaSources  []MediaSourceInfo `json:"MediaSources"`
	PlaySessionId string            `json:"PlaySessionId"`
	ErrorCode     string            `json:"ErrorCode,omitempty"`
}

type MediaSourceInfo struct {
	Id                      string            `json:"Id"`
	Name                    string            `json:"Name,omitempty"`
	Path                    string            `json:"Path"`
	DirectStreamUrl         string            `json:"DirectStreamUrl,omitempty"`
	Container               string            `json:"Container,omitempty"`
	Protocol                string            `json:"Protocol"`
	Type                    string            `json:"Type"`
	IsRemote                bool              `json:"IsRemote"`
	SupportsDirectPlay      bool              `json:"SupportsDirectPlay"`
	SupportsDirectStream    bool              `json:"SupportsDirectStream"`
	SupportsTranscoding     bool              `json:"SupportsTranscoding"`
	SupportsProbing         bool              `json:"SupportsProbing"`
	VideoType               string            `json:"VideoType,omitempty"`
	Size                    *int64            `json:"Size,omitempty"`
	RunTimeTicks            *int64            `json:"RunTimeTicks,omitempty"`
	Bitrate                 *int64            `json:"Bitrate,omitempty"`
	ETag                    string            `json:"ETag,omitempty"`
	Formats                 []string          `json:"Formats"`
	RequiredHttpHeaders     map[string]string `json:"RequiredHttpHeaders"`
	MediaAttachments        []any             `json:"MediaAttachments"`
	MediaStreams            []MediaStreamInfo `json:"MediaStreams"`
	DefaultAudioStreamIndex *int              `json:"DefaultAudioStreamIndex,omitempty"`
	TranscodingUrl          string            `json:"TranscodingUrl,omitempty"`
	TranscodingSubProtocol  string            `json:"TranscodingSubProtocol,omitempty"`
	TranscodingContainer    string            `json:"TranscodingContainer,omitempty"`
}

type MediaStreamInfo struct {
	Codec                  string `json:"Codec,omitempty"`
	Language               string `json:"Language,omitempty"`
	DisplayTitle           string `json:"DisplayTitle,omitempty"`
	Type                   string `json:"Type"`
	Index                  int    `json:"Index"`
	IsDefault              bool   `json:"IsDefault"`
	IsForced               bool   `json:"IsForced"`
	IsExternal             bool   `json:"IsExternal"`
	IsExternalUrl          bool   `json:"IsExternalUrl"`
	IsTextSubtitleStream   bool   `json:"IsTextSubtitleStream"`
	SupportsExternalStream bool   `json:"SupportsExternalStream"`
	DeliveryMethod         string `json:"DeliveryMethod,omitempty"`
	DeliveryUrl            string `json:"DeliveryUrl,omitempty"`
	Width                  int    `json:"Width,omitempty"`
	Height                 int    `json:"Height,omitempty"`
	Channels               int    `json:"Channels,omitempty"`
	BitRate                int64  `json:"BitRate,omitempty"`
}

type CodecProfile struct {
	Type       string             `json:"Type,omitempty"`
	Codec      string             `json:"Codec,omitempty"`
	Conditions []ProfileCondition `json:"Conditions,omitempty"`
}

type ProfileCondition struct {
	Condition  string `json:"Condition,omitempty"`
	Property   string `json:"Property,omitempty"`
	Value      string `json:"Value,omitempty"`
	IsRequired bool   `json:"IsRequired,omitempty"`
}

type ContainerProfile struct {
	Type         string             `json:"Type,omitempty"`
	Container    string             `json:"Container,omitempty"`
	SubContainer string             `json:"SubContainer,omitempty"`
	Conditions   []ProfileCondition `json:"Conditions,omitempty"`
}

type DeviceProfile struct {
	Name                string               `json:"Name,omitempty"`
	MaxStreamingBitrate int64                `json:"MaxStreamingBitrate,omitempty"`
	CodecProfiles       []CodecProfile       `json:"CodecProfiles,omitempty"`
	ContainerProfiles   []ContainerProfile   `json:"ContainerProfiles,omitempty"`
	DirectPlayProfiles  []DirectPlayProfile  `json:"DirectPlayProfiles,omitempty"`
	TranscodingProfiles []TranscodingProfile `json:"TranscodingProfiles,omitempty"`
	SubtitleProfiles    []SubtitleProfile    `json:"SubtitleProfiles,omitempty"`
}

type DirectPlayProfile struct {
	Container  string `json:"Container,omitempty"`
	AudioCodec string `json:"AudioCodec,omitempty"`
	VideoCodec string `json:"VideoCodec,omitempty"`
	Type       string `json:"Type,omitempty"`
}

type TranscodingProfile struct {
	Container            string `json:"Container,omitempty"`
	Type                 string `json:"Type,omitempty"`
	VideoCodec           string `json:"VideoCodec,omitempty"`
	AudioCodec           string `json:"AudioCodec,omitempty"`
	Protocol             string `json:"Protocol,omitempty"`
	Context              string `json:"Context,omitempty"`
	MaxAudioChannels     string `json:"MaxAudioChannels,omitempty"`
	EnableMpegtsM2TsMode bool   `json:"EnableMpegtsM2TsMode,omitempty"`
	BreakOnNonKeyFrames  bool   `json:"BreakOnNonKeyFrames,omitempty"`
}

type SubtitleProfile struct {
	Format string `json:"Format,omitempty"`
	Method string `json:"Method,omitempty"`
}

type PlaybackProgressInfo struct {
	UserId                 string `json:"UserId,omitempty"`
	ItemId                 string `json:"ItemId"`
	MediaSourceId          string `json:"MediaSourceId,omitempty"`
	PlaySessionId          string `json:"PlaySessionId,omitempty"`
	PositionTicks          int64  `json:"PositionTicks,omitempty"`
	AudioStreamIndex       *int   `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex    *int   `json:"SubtitleStreamIndex,omitempty"`
	CanSeek                bool   `json:"CanSeek,omitempty"`
	PlaybackStartTimeTicks int64  `json:"PlaybackStartTimeTicks,omitempty"`
	IsPaused               bool   `json:"IsPaused,omitempty"`
	IsMuted                bool   `json:"IsMuted,omitempty"`
	PlayMethod             string `json:"PlayMethod,omitempty"`
}

type DisplayPreferencesDto struct {
	Id                 string            `json:"Id"`
	ViewType           string            `json:"ViewType"`
	SortBy             string            `json:"SortBy"`
	IndexBy            string            `json:"IndexBy"`
	RememberIndexing   bool              `json:"RememberIndexing"`
	PrimaryImageHeight int32             `json:"PrimaryImageHeight"`
	PrimaryImageWidth  int32             `json:"PrimaryImageWidth"`
	CustomPrefs        map[string]string `json:"CustomPrefs"`
	ScrollDirection    string            `json:"ScrollDirection"`
	ShowBackdrop       bool              `json:"ShowBackdrop"`
	RememberSorting    bool              `json:"RememberSorting"`
	SortOrder          string            `json:"SortOrder"`
	ShowSidebar        bool              `json:"ShowSidebar"`
	Client             string            `json:"Client"`
}
