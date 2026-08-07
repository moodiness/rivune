package jellyfin

const (
	CompatibilityVersion = "10.11.0"
	CompatibilityProduct = "Rivune Jellyfin Compatibility"
)

type PublicSystemInfo struct {
	Id                     string `json:"Id"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

type SystemInfo struct {
	PublicSystemInfo
	OperatingSystem string `json:"OperatingSystem,omitempty"`
}

type SystemEndpointInfo struct {
	IsLocal     bool `json:"IsLocal"`
	IsInNetwork bool `json:"IsInNetwork"`
}

type AuthenticateByName struct {
	Username   string `json:"Username,omitempty"`
	UserName   string `json:"UserName,omitempty"`
	Pw         string `json:"Pw,omitempty"`
	Password   string `json:"Password,omitempty"`
	ProfilePin string `json:"ProfilePin,omitempty"`
}

type AuthenticationResult struct {
	User        UserDto        `json:"User"`
	SessionInfo SessionInfoDto `json:"SessionInfo"`
	AccessToken string         `json:"AccessToken"`
	ServerId    string         `json:"ServerId"`
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
	IsAdministrator                bool `json:"IsAdministrator"`
	IsDisabled                     bool `json:"IsDisabled"`
	EnablePlayback                 bool `json:"EnablePlayback"`
	EnableAudioPlaybackTranscoding bool `json:"EnableAudioPlaybackTranscoding"`
	EnableVideoPlaybackTranscoding bool `json:"EnableVideoPlaybackTranscoding"`
}

type UserConfiguration struct {
	PlayDefaultAudioTrack bool   `json:"PlayDefaultAudioTrack"`
	SubtitleMode          string `json:"SubtitleMode"`
}

type SessionInfoDto struct {
	Id                 string       `json:"Id"`
	UserId             string       `json:"UserId"`
	UserName           string       `json:"UserName"`
	Client             string       `json:"Client"`
	DeviceName         string       `json:"DeviceName"`
	DeviceId           string       `json:"DeviceId"`
	ApplicationVersion string       `json:"ApplicationVersion"`
	NowPlayingItem     *BaseItemDto `json:"NowPlayingItem,omitempty"`
}

type QueryResult[T any] struct {
	Items            []T `json:"Items"`
	TotalRecordCount int `json:"TotalRecordCount"`
	StartIndex       int `json:"StartIndex"`
}

type BaseItemDto struct {
	Id                string            `json:"Id"`
	ServerId          string            `json:"ServerId"`
	Name              string            `json:"Name"`
	SortName          string            `json:"SortName,omitempty"`
	Type              string            `json:"Type"`
	MediaType         string            `json:"MediaType,omitempty"`
	IsFolder          bool              `json:"IsFolder"`
	IsPlayable        bool              `json:"IsPlayable"`
	CollectionType    string            `json:"CollectionType,omitempty"`
	ParentId          string            `json:"ParentId,omitempty"`
	SeriesId          string            `json:"SeriesId,omitempty"`
	SeasonId          string            `json:"SeasonId,omitempty"`
	SeriesName        string            `json:"SeriesName,omitempty"`
	SeasonName        string            `json:"SeasonName,omitempty"`
	IndexNumber       *int              `json:"IndexNumber,omitempty"`
	ParentIndexNumber *int              `json:"ParentIndexNumber,omitempty"`
	Overview          string            `json:"Overview,omitempty"`
	PremiereDate      string            `json:"PremiereDate,omitempty"`
	ProductionYear    *int              `json:"ProductionYear,omitempty"`
	RunTimeTicks      *int64            `json:"RunTimeTicks,omitempty"`
	Genres            []string          `json:"Genres"`
	CommunityRating   *float32          `json:"CommunityRating,omitempty"`
	ProviderIds       map[string]string `json:"ProviderIds,omitempty"`
	ImageTags         map[string]string `json:"ImageTags,omitempty"`
	BackdropImageTags []string          `json:"BackdropImageTags"`
	UserData          *UserItemDataDto  `json:"UserData,omitempty"`
}

type UserItemDataDto struct {
	PlaybackPositionTicks int64  `json:"PlaybackPositionTicks"`
	PlayCount             int    `json:"PlayCount"`
	IsFavorite            bool   `json:"IsFavorite"`
	Played                bool   `json:"Played"`
	Key                   string `json:"Key"`
	LastPlayedDate        string `json:"LastPlayedDate,omitempty"`
}

type SearchHintDto struct {
	ItemId           string            `json:"ItemId"`
	Id               string            `json:"Id"`
	Name             string            `json:"Name"`
	Type             string            `json:"Type"`
	MediaType        string            `json:"MediaType,omitempty"`
	PrimaryImageTag  string            `json:"PrimaryImageTag,omitempty"`
	ThumbImageTag    string            `json:"ThumbImageTag,omitempty"`
	BackdropImageTag string            `json:"BackdropImageTag,omitempty"`
	ProductionYear   *int              `json:"ProductionYear,omitempty"`
	RunTimeTicks     *int64            `json:"RunTimeTicks,omitempty"`
	ProviderIds      map[string]string `json:"ProviderIds,omitempty"`
}

type SearchHintResult struct {
	SearchHints      []SearchHintDto `json:"SearchHints"`
	TotalRecordCount int             `json:"TotalRecordCount"`
}

type PlaybackInfoRequest struct {
	UserId              string        `json:"UserId,omitempty"`
	StartTimeTicks      int64         `json:"StartTimeTicks,omitempty"`
	IsPlayback          bool          `json:"IsPlayback,omitempty"`
	AutoOpenLiveStream  bool          `json:"AutoOpenLiveStream,omitempty"`
	MaxStreamingBitrate int64         `json:"MaxStreamingBitrate,omitempty"`
	MediaSourceId       string        `json:"MediaSourceId,omitempty"`
	DeviceProfile       DeviceProfile `json:"DeviceProfile"`
}

type PlaybackInfoResponse struct {
	MediaSources  []MediaSourceInfo `json:"MediaSources"`
	PlaySessionId string            `json:"PlaySessionId"`
	ErrorCode     string            `json:"ErrorCode,omitempty"`
}

type MediaSourceInfo struct {
	Id                     string `json:"Id"`
	Name                   string `json:"Name,omitempty"`
	Path                   string `json:"Path"`
	Container              string `json:"Container,omitempty"`
	Protocol               string `json:"Protocol"`
	Type                   string `json:"Type"`
	IsRemote               bool   `json:"IsRemote"`
	SupportsDirectPlay     bool   `json:"SupportsDirectPlay"`
	SupportsDirectStream   bool   `json:"SupportsDirectStream"`
	SupportsTranscoding    bool   `json:"SupportsTranscoding"`
	RunTimeTicks           *int64 `json:"RunTimeTicks,omitempty"`
	Bitrate                *int64 `json:"Bitrate,omitempty"`
	ETag                   string `json:"ETag,omitempty"`
	TranscodingUrl         string `json:"TranscodingUrl,omitempty"`
	TranscodingSubProtocol string `json:"TranscodingSubProtocol,omitempty"`
	TranscodingContainer   string `json:"TranscodingContainer,omitempty"`
}

type DeviceProfile struct {
	Name                string               `json:"Name,omitempty"`
	MaxStreamingBitrate int64                `json:"MaxStreamingBitrate,omitempty"`
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
	PlaybackStartTimeTicks int64  `json:"PlaybackStartTimeTicks,omitempty"`
	IsPaused               bool   `json:"IsPaused,omitempty"`
	IsMuted                bool   `json:"IsMuted,omitempty"`
	PlayMethod             string `json:"PlayMethod,omitempty"`
}

type DisplayPreferencesDto struct {
	Id               string            `json:"Id"`
	ViewType         string            `json:"ViewType"`
	RememberIndexing bool              `json:"RememberIndexing"`
	IndexBy          string            `json:"IndexBy"`
	CustomPrefs      map[string]string `json:"CustomPrefs"`
	ScrollDirection  string            `json:"ScrollDirection"`
}
