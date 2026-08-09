package jellyfin

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Route string

const (
	RoutePublicSystemInfo         Route = "public-system-info"
	RouteSystemPing               Route = "system-ping"
	RouteSystemPingPost           Route = "system-ping-post"
	RouteSystemEndpoint           Route = "system-endpoint"
	RouteQuickConnectEnabled      Route = "quick-connect-enabled"
	RouteAuthenticateByName       Route = "authenticate-by-name"
	RoutePublicUsers              Route = "public-users"
	RouteSystemInfo               Route = "system-info"
	RouteCurrentUser              Route = "current-user"
	RouteUser                     Route = "user"
	RouteUsers                    Route = "users"
	RouteUserImage                Route = "user-image"
	RouteUserImageHead            Route = "user-image-head"
	RouteUserPrimaryImage         Route = "user-primary-image"
	RouteUserPrimaryImageHead     Route = "user-primary-image-head"
	RouteSessions                 Route = "sessions"
	RouteViewing                  Route = "viewing"
	RouteLogout                   Route = "logout"
	RouteSessionCapabilitiesFull  Route = "session-capabilities-full"
	RouteSessionCapabilities      Route = "session-capabilities"
	RouteActiveEncodings          Route = "active-encodings"
	RouteClientLog                Route = "client-log"
	RouteSocket                   Route = "socket"
	RouteSyncPlayList             Route = "sync-play-list"
	RoutePlaybackBitrateTest      Route = "playback-bitrate-test"
	RoutePlugins                  Route = "plugins"
	RoutePackages                 Route = "packages"
	RouteBrandingConfiguration    Route = "branding-configuration"
	RouteBrandingSplashscreen     Route = "branding-splashscreen"
	RouteDisplayPreferences       Route = "display-preferences"
	RouteDisplayPreferencesUpdate Route = "display-preferences-update"
	RouteGroupingOptions          Route = "grouping-options"
	RouteUserViews                Route = "user-views"
	RouteViews                    Route = "views"
	RouteVirtualFolders           Route = "virtual-folders"
	RouteSelectableMediaFolders   Route = "selectable-media-folders"
	RouteItems                    Route = "items"
	RouteUserItems                Route = "user-items"
	RouteLatestItems              Route = "latest-items"
	RouteUserLatestItems          Route = "user-latest-items"
	RouteItem                     Route = "item"
	RouteUserItem                 Route = "user-item"
	RouteSeasons                  Route = "seasons"
	RouteEpisodes                 Route = "episodes"
	RouteSearchHints              Route = "search-hints"
	RouteUserSearchHints          Route = "user-search-hints"
	RouteItemsFilters             Route = "items-filters"
	RouteItemsFilters2            Route = "items-filters-2"
	RouteSuggestions              Route = "suggestions"
	RouteSimilarItems             Route = "similar-items"
	RouteSimilarMovies            Route = "similar-movies"
	RouteSimilarShows             Route = "similar-shows"
	RouteGenres                   Route = "genres"
	RouteGenre                    Route = "genre"
	RoutePersons                  Route = "persons"
	RoutePerson                   Route = "person"
	RouteStudios                  Route = "studios"
	RouteArtists                  Route = "artists"
	RouteUpcomingShows            Route = "upcoming-shows"
	RouteMovieRecommendations     Route = "movie-recommendations"
	RouteMediaSegments            Route = "media-segments"
	RouteTrickplayImage           Route = "trickplay-image"
	RouteTrickplayImageHead       Route = "trickplay-image-head"
	RouteThemeMedia               Route = "theme-media"
	RouteThemeSongs               Route = "theme-songs"
	RouteSpecialFeatures          Route = "special-features"
	RouteIntros                   Route = "intros"
	RouteLocalTrailers            Route = "local-trailers"
	RouteLegacyThemeMedia         Route = "legacy-theme-media"
	RouteLegacyThemeSongs         Route = "legacy-theme-songs"
	RouteLegacySpecialFeatures    Route = "legacy-special-features"
	RouteLegacyIntros             Route = "legacy-intros"
	RouteLegacyLocalTrailers      Route = "legacy-local-trailers"
	RouteImageInfos               Route = "image-infos"
	RouteImage                    Route = "image"
	RouteImageHead                Route = "image-head"
	RouteIndexedImage             Route = "indexed-image"
	RouteIndexedImageHead         Route = "indexed-image-head"
	RoutePlaybackInfo             Route = "playback-info"
	RoutePlaybackInfoPost         Route = "playback-info-post"
	RouteUserPlaybackInfo         Route = "user-playback-info"
	RouteUserPlaybackInfoPost     Route = "user-playback-info-post"
	RouteStream                   Route = "stream"
	RouteStreamHead               Route = "stream-head"
	RouteContainerStream          Route = "container-stream"
	RouteContainerStreamHead      Route = "container-stream-head"
	RouteMasterPlaylist           Route = "master-playlist"
	RouteMasterPlaylistHead       Route = "master-playlist-head"
	RouteMainPlaylist             Route = "main-playlist"
	RouteMainPlaylistHead         Route = "main-playlist-head"
	RouteHLSSegment               Route = "hls-segment"
	RouteHLSSegmentHead           Route = "hls-segment-head"
	RouteLegacyHLSSegment         Route = "legacy-hls-segment"
	RouteLegacyHLSSegmentHead     Route = "legacy-hls-segment-head"
	RouteSubtitleStream           Route = "subtitle-stream"
	RouteSubtitleStreamHead       Route = "subtitle-stream-head"
	RouteSubtitleStreamAt         Route = "subtitle-stream-at"
	RouteSubtitleStreamAtHead     Route = "subtitle-stream-at-head"
	RouteItemDownload             Route = "item-download"
	RouteItemDownloadHead         Route = "item-download-head"
	RoutePlaying                  Route = "playing"
	RoutePlayingProgress          Route = "playing-progress"
	RoutePlayingStopped           Route = "playing-stopped"
	RoutePlayingPing              Route = "playing-ping"
	RoutePlayedItem               Route = "played-item"
	RoutePlayedItemDelete         Route = "played-item-delete"
	RouteUserPlayedItem           Route = "user-played-item"
	RouteUserPlayedItemDelete     Route = "user-played-item-delete"
	RouteUserData                 Route = "user-data"
	RouteUserDataUpdate           Route = "user-data-update"
	RouteLegacyUserData           Route = "legacy-user-data"
	RouteLegacyUserDataUpdate     Route = "legacy-user-data-update"
	RouteFavoriteItem             Route = "favorite-item"
	RouteFavoriteItemDelete       Route = "favorite-item-delete"
	RouteLegacyFavoriteItem       Route = "legacy-favorite-item"
	RouteLegacyFavoriteItemDelete Route = "legacy-favorite-item-delete"
	RouteResumeItems              Route = "resume-items"
	RouteUserResumeItems          Route = "user-resume-items"
	RouteNextUp                   Route = "next-up"
)

type RouteSpec struct {
	Route   Route
	Method  string
	Pattern string
}

var ErrInvalidDependencies = errors.New("invalid jellyfin compatibility dependencies")

type ServerInfo struct {
	ID             ServerID
	Name           string
	LocalAddress   string
	RuntimeVersion string
}

var routeDefinitions = []RouteSpec{
	{RoutePublicSystemInfo, http.MethodGet, "/System/Info/Public"},
	{RouteSystemPing, http.MethodGet, "/System/Ping"},
	{RouteSystemPingPost, http.MethodPost, "/System/Ping"},
	{RouteSystemEndpoint, http.MethodGet, "/System/Endpoint"},
	{RouteQuickConnectEnabled, http.MethodGet, "/QuickConnect/Enabled"},
	{RouteAuthenticateByName, http.MethodPost, "/Users/AuthenticateByName"},
	{RoutePublicUsers, http.MethodGet, "/Users/Public"},
	{RouteSystemInfo, http.MethodGet, "/System/Info"},
	{RouteCurrentUser, http.MethodGet, "/Users/Me"},
	{RouteUser, http.MethodGet, "/Users/{id}"},
	{RouteUsers, http.MethodGet, "/Users"},
	{RouteUserImage, http.MethodGet, "/UserImage"},
	{RouteUserImageHead, http.MethodHead, "/UserImage"},
	{RouteUserPrimaryImage, http.MethodGet, "/Users/{userId}/Images/Primary"},
	{RouteUserPrimaryImageHead, http.MethodHead, "/Users/{userId}/Images/Primary"},
	{RouteSessions, http.MethodGet, "/Sessions"},
	{RouteViewing, http.MethodPost, "/Sessions/Viewing"},
	{RouteLogout, http.MethodPost, "/Sessions/Logout"},
	{RouteSessionCapabilitiesFull, http.MethodPost, "/Sessions/Capabilities/Full"},
	{RouteSessionCapabilities, http.MethodPost, "/Sessions/Capabilities"},
	{RouteActiveEncodings, http.MethodDelete, "/Videos/ActiveEncodings"},
	{RouteClientLog, http.MethodPost, "/ClientLog/Document"},
	{RouteSocket, http.MethodGet, "/socket"},
	{RouteSyncPlayList, http.MethodGet, "/SyncPlay/List"},
	{RoutePlaybackBitrateTest, http.MethodGet, "/Playback/BitrateTest"},
	{RoutePlugins, http.MethodGet, "/Plugins"},
	{RoutePackages, http.MethodGet, "/Packages"},
	{RouteBrandingConfiguration, http.MethodGet, "/Branding/Configuration"},
	{RouteBrandingSplashscreen, http.MethodGet, "/Branding/Splashscreen"},
	{RouteDisplayPreferences, http.MethodGet, "/DisplayPreferences/{displayPreferencesId}"},
	{RouteDisplayPreferencesUpdate, http.MethodPost, "/DisplayPreferences/{displayPreferencesId}"},
	{RouteGroupingOptions, http.MethodGet, "/UserViews/GroupingOptions"},
	{RouteUserViews, http.MethodGet, "/Users/{id}/Views"},
	{RouteViews, http.MethodGet, "/UserViews"},
	{RouteVirtualFolders, http.MethodGet, "/Library/VirtualFolders"},
	{RouteSelectableMediaFolders, http.MethodGet, "/Library/SelectableMediaFolders"},
	{RouteItems, http.MethodGet, "/Items"},
	{RouteUserItems, http.MethodGet, "/Users/{id}/Items"},
	{RouteLatestItems, http.MethodGet, "/Items/Latest"},
	{RouteUserLatestItems, http.MethodGet, "/Users/{id}/Items/Latest"},
	{RouteItem, http.MethodGet, "/Items/{id}"},
	{RouteUserItem, http.MethodGet, "/Users/{userId}/Items/{itemId}"},
	{RouteSeasons, http.MethodGet, "/Shows/{seriesId}/Seasons"},
	{RouteEpisodes, http.MethodGet, "/Shows/{seriesId}/Episodes"},
	{RouteSearchHints, http.MethodGet, "/Search/Hints"},
	{RouteUserSearchHints, http.MethodGet, "/Users/{id}/Search/Hints"},
	{RouteItemsFilters, http.MethodGet, "/Items/Filters"},
	{RouteItemsFilters2, http.MethodGet, "/Items/Filters2"},
	{RouteSuggestions, http.MethodGet, "/Items/Suggestions"},
	{RouteSimilarItems, http.MethodGet, "/Items/{itemId}/Similar"},
	{RouteSimilarMovies, http.MethodGet, "/Movies/{itemId}/Similar"},
	{RouteSimilarShows, http.MethodGet, "/Shows/{itemId}/Similar"},
	{RouteGenres, http.MethodGet, "/Genres"},
	{RouteGenre, http.MethodGet, "/Genres/{genreName}"},
	{RoutePersons, http.MethodGet, "/Persons"},
	{RoutePerson, http.MethodGet, "/Persons/{name}"},
	{RouteStudios, http.MethodGet, "/Studios"},
	{RouteArtists, http.MethodGet, "/Artists"},
	{RouteUpcomingShows, http.MethodGet, "/Shows/Upcoming"},
	{RouteMovieRecommendations, http.MethodGet, "/Movies/Recommendations"},
	{RouteMediaSegments, http.MethodGet, "/MediaSegments/{itemId}"},
	{RouteTrickplayImage, http.MethodGet, "/Videos/{itemId}/Trickplay/{width}/{index}.jpg"},
	{RouteTrickplayImageHead, http.MethodHead, "/Videos/{itemId}/Trickplay/{width}/{index}.jpg"},
	{RouteThemeMedia, http.MethodGet, "/Items/{itemId}/ThemeMedia"},
	{RouteThemeSongs, http.MethodGet, "/Items/{itemId}/ThemeSongs"},
	{RouteSpecialFeatures, http.MethodGet, "/Items/{itemId}/SpecialFeatures"},
	{RouteIntros, http.MethodGet, "/Items/{itemId}/Intros"},
	{RouteLocalTrailers, http.MethodGet, "/Items/{itemId}/LocalTrailers"},
	{RouteLegacyThemeMedia, http.MethodGet, "/Users/{userId}/Items/{itemId}/ThemeMedia"},
	{RouteLegacyThemeSongs, http.MethodGet, "/Users/{userId}/Items/{itemId}/ThemeSongs"},
	{RouteLegacySpecialFeatures, http.MethodGet, "/Users/{userId}/Items/{itemId}/SpecialFeatures"},
	{RouteLegacyIntros, http.MethodGet, "/Users/{userId}/Items/{itemId}/Intros"},
	{RouteLegacyLocalTrailers, http.MethodGet, "/Users/{userId}/Items/{itemId}/LocalTrailers"},
	{RouteImageInfos, http.MethodGet, "/Items/{id}/Images"},
	{RouteImage, http.MethodGet, "/Items/{id}/Images/{type}"},
	{RouteImageHead, http.MethodHead, "/Items/{id}/Images/{type}"},
	{RouteIndexedImage, http.MethodGet, "/Items/{id}/Images/{type}/{index}"},
	{RouteIndexedImageHead, http.MethodHead, "/Items/{id}/Images/{type}/{index}"},
	{RoutePlaybackInfo, http.MethodGet, "/Items/{id}/PlaybackInfo"},
	{RoutePlaybackInfoPost, http.MethodPost, "/Items/{id}/PlaybackInfo"},
	{RouteUserPlaybackInfo, http.MethodGet, "/Users/{userId}/Items/{id}/PlaybackInfo"},
	{RouteUserPlaybackInfoPost, http.MethodPost, "/Users/{userId}/Items/{id}/PlaybackInfo"},
	{RouteStream, http.MethodGet, "/Videos/{id}/stream"},
	{RouteStreamHead, http.MethodHead, "/Videos/{id}/stream"},
	{RouteContainerStream, http.MethodGet, "/Videos/{id}/stream.{container}"},
	{RouteContainerStreamHead, http.MethodHead, "/Videos/{id}/stream.{container}"},
	{RouteMasterPlaylist, http.MethodGet, "/Videos/{id}/master.m3u8"},
	{RouteMasterPlaylistHead, http.MethodHead, "/Videos/{id}/master.m3u8"},
	{RouteMainPlaylist, http.MethodGet, "/Videos/{id}/main.m3u8"},
	{RouteMainPlaylistHead, http.MethodHead, "/Videos/{id}/main.m3u8"},
	{RouteHLSSegment, http.MethodGet, "/Videos/{id}/hls1/{playlistId}/{segmentId}.{container}"},
	{RouteHLSSegmentHead, http.MethodHead, "/Videos/{id}/hls1/{playlistId}/{segmentId}.{container}"},
	{RouteLegacyHLSSegment, http.MethodGet, "/Videos/{id}/hls/{playlistId}/{segmentId}.{container}"},
	{RouteLegacyHLSSegmentHead, http.MethodHead, "/Videos/{id}/hls/{playlistId}/{segmentId}.{container}"},
	{RouteSubtitleStream, http.MethodGet, "/Videos/{id}/{mediaSourceId}/Subtitles/{subtitleIndex}/Stream.{format}"},
	{RouteSubtitleStreamHead, http.MethodHead, "/Videos/{id}/{mediaSourceId}/Subtitles/{subtitleIndex}/Stream.{format}"},
	{RouteSubtitleStreamAt, http.MethodGet, "/Videos/{id}/{mediaSourceId}/Subtitles/{subtitleIndex}/{startPositionTicks}/Stream.{format}"},
	{RouteSubtitleStreamAtHead, http.MethodHead, "/Videos/{id}/{mediaSourceId}/Subtitles/{subtitleIndex}/{startPositionTicks}/Stream.{format}"},
	{RouteItemDownload, http.MethodGet, "/Items/{id}/Download"},
	{RouteItemDownloadHead, http.MethodHead, "/Items/{id}/Download"},
	{RoutePlaying, http.MethodPost, "/Sessions/Playing"},
	{RoutePlayingProgress, http.MethodPost, "/Sessions/Playing/Progress"},
	{RoutePlayingStopped, http.MethodPost, "/Sessions/Playing/Stopped"},
	{RoutePlayingPing, http.MethodPost, "/Sessions/Playing/Ping"},
	{RoutePlayedItem, http.MethodPost, "/Users/{userId}/PlayedItems/{itemId}"},
	{RoutePlayedItemDelete, http.MethodDelete, "/Users/{userId}/PlayedItems/{itemId}"},
	{RouteUserPlayedItem, http.MethodPost, "/UserPlayedItems/{itemId}"},
	{RouteUserPlayedItemDelete, http.MethodDelete, "/UserPlayedItems/{itemId}"},
	{RouteUserData, http.MethodGet, "/UserItems/{itemId}/UserData"},
	{RouteUserDataUpdate, http.MethodPost, "/UserItems/{itemId}/UserData"},
	{RouteLegacyUserData, http.MethodGet, "/Users/{userId}/Items/{itemId}/UserData"},
	{RouteLegacyUserDataUpdate, http.MethodPost, "/Users/{userId}/Items/{itemId}/UserData"},
	{RouteFavoriteItem, http.MethodPost, "/UserFavoriteItems/{itemId}"},
	{RouteFavoriteItemDelete, http.MethodDelete, "/UserFavoriteItems/{itemId}"},
	{RouteLegacyFavoriteItem, http.MethodPost, "/Users/{userId}/FavoriteItems/{itemId}"},
	{RouteLegacyFavoriteItemDelete, http.MethodDelete, "/Users/{userId}/FavoriteItems/{itemId}"},
	{RouteResumeItems, http.MethodGet, "/Users/{id}/Items/Resume"},
	{RouteUserResumeItems, http.MethodGet, "/UserItems/Resume"},
	{RouteNextUp, http.MethodGet, "/Shows/NextUp"},
}

type Dependencies struct {
	ServerInfo          ServerInfo
	Authentication      Authentication
	AuthenticatedPolicy AuthenticatedRequestPolicy
	Catalog             CatalogReader
	Collections         CollectionReader
	Artwork             ArtworkDelivery
	Playback            PlaybackDelivery
	MediaSegments       MediaSegmentReader
	Watchstate          Watchstate
	DisplayPreferences  DisplayPreferences
	Logger              *slog.Logger
	Debug               bool

	// Handlers overrides built-ins for focused tests. Unknown or nil overrides
	// are rejected; built-ins are installed only when their dependencies exist.
	Handlers map[Route]http.Handler
}

type Handler struct {
	serverInfo         ServerInfo
	authentication     Authentication
	catalog            CatalogReader
	collections        CollectionReader
	artwork            ArtworkDelivery
	playback           PlaybackDelivery
	mediaSegments      MediaSegmentReader
	watchstate         Watchstate
	displayPreferences DisplayPreferences
	playSessions       *playSessionRegistry
	bootstrap          *bootstrapRegistry
	logger             *slog.Logger
	handlers           map[Route]http.Handler
	routes             map[string][]routeBinding
}

var _ http.Handler = (*Handler)(nil)

func New(dependencies Dependencies) (*Handler, error) {
	known := make(map[Route]struct{}, len(routeDefinitions))
	for _, definition := range routeDefinitions {
		known[definition.Route] = struct{}{}
	}
	for route, implementation := range dependencies.Handlers {
		if _, ok := known[route]; !ok || implementation == nil {
			return nil, ErrInvalidDependencies
		}
	}
	handler := &Handler{
		serverInfo:         dependencies.ServerInfo,
		authentication:     newPolicyAuthentication(dependencies.Authentication, dependencies.AuthenticatedPolicy),
		catalog:            dependencies.Catalog,
		collections:        dependencies.Collections,
		artwork:            dependencies.Artwork,
		playback:           dependencies.Playback,
		mediaSegments:      dependencies.MediaSegments,
		watchstate:         dependencies.Watchstate,
		displayPreferences: dependencies.DisplayPreferences,
		logger:             dependencies.Logger,
		handlers:           make(map[Route]http.Handler, len(routeDefinitions)),
		routes:             make(map[string][]routeBinding, 4),
	}
	if _, serverReady := handler.publicSystemInfo(); serverReady && handler.authentication != nil {
		handler.bootstrap = newBootstrapRegistry()
		if handler.catalog != nil && handler.playback != nil {
			handler.playSessions = newPlaySessionRegistry(handler.playback)
		}
	}
	handler.installBuiltInHandlers()
	for route, implementation := range dependencies.Handlers {
		handler.handlers[route] = implementation
	}
	for _, definition := range routeDefinitions {
		implementation, ok := handler.handlers[definition.Route]
		if !ok {
			continue
		}
		handler.routes[definition.Method] = append(handler.routes[definition.Method], routeBinding{
			definition:     definition,
			implementation: traceRoute(handler.logger, dependencies.Debug, definition, implementation),
		})
	}
	return handler, nil
}
func (handler *Handler) installBuiltInHandlers() {
	_, serverReady := handler.publicSystemInfo()
	if serverReady {
		handler.handlers[RoutePublicSystemInfo] = http.HandlerFunc(handler.handlePublicSystemInfo)
		handler.handlers[RouteSystemPing] = http.HandlerFunc(handler.handleSystemPing)
		handler.handlers[RouteSystemPingPost] = http.HandlerFunc(handler.handleSystemPing)
		handler.handlers[RouteQuickConnectEnabled] = http.HandlerFunc(handler.handleQuickConnectEnabled)
		handler.handlers[RoutePublicUsers] = http.HandlerFunc(handler.handlePublicUsers)
		handler.handlers[RouteBrandingConfiguration] = http.HandlerFunc(handler.handleBrandingConfiguration)
		handler.handlers[RouteBrandingSplashscreen] = http.HandlerFunc(handler.handleBrandingSplashscreen)
	}
	if serverReady && handler.authentication != nil {
		handler.handlers[RouteAuthenticateByName] = http.HandlerFunc(handler.handleAuthenticateByName)
		handler.handlers[RouteSystemEndpoint] = http.HandlerFunc(handler.handleSystemEndpoint)
		handler.handlers[RouteSystemInfo] = http.HandlerFunc(handler.handleSystemInfo)
		handler.handlers[RouteCurrentUser] = http.HandlerFunc(handler.handleCurrentUser)
		handler.handlers[RouteUser] = http.HandlerFunc(handler.handleUser)
		handler.handlers[RouteUsers] = http.HandlerFunc(handler.handleUsers)
		handler.handlers[RouteUserImage] = http.HandlerFunc(handler.handleUserImage)
		handler.handlers[RouteUserImageHead] = http.HandlerFunc(handler.handleUserImage)
		handler.handlers[RouteUserPrimaryImage] = http.HandlerFunc(handler.handleUserImage)
		handler.handlers[RouteUserPrimaryImageHead] = http.HandlerFunc(handler.handleUserImage)
		handler.handlers[RouteSessions] = http.HandlerFunc(handler.handleSessions)
		handler.handlers[RouteLogout] = http.HandlerFunc(handler.handleLogout)
		handler.handlers[RouteSessionCapabilitiesFull] = http.HandlerFunc(handler.handleSessionCapabilitiesFull)
		handler.handlers[RouteSessionCapabilities] = http.HandlerFunc(handler.handleSessionCapabilities)
		handler.handlers[RouteActiveEncodings] = http.HandlerFunc(handler.handleActiveEncodings)
		handler.handlers[RouteClientLog] = http.HandlerFunc(handler.handleClientLog)
		handler.handlers[RouteSocket] = http.HandlerFunc(handler.handleSocket)
		handler.handlers[RouteSyncPlayList] = http.HandlerFunc(handler.handleSyncPlayList)
		handler.handlers[RoutePlaybackBitrateTest] = http.HandlerFunc(handler.handlePlaybackBitrateTest)
		handler.handlers[RoutePlugins] = http.HandlerFunc(handler.handlePlugins)
		handler.handlers[RoutePackages] = http.HandlerFunc(handler.handlePackages)
		handler.handlers[RouteDisplayPreferences] = http.HandlerFunc(handler.handleDisplayPreferences)
		handler.handlers[RouteDisplayPreferencesUpdate] = http.HandlerFunc(handler.handleDisplayPreferencesUpdate)
		handler.handlers[RouteGroupingOptions] = http.HandlerFunc(handler.handleGroupingOptions)
	}
	if serverReady && handler.authentication != nil && handler.catalog != nil {
		handler.handlers[RouteViewing] = http.HandlerFunc(handler.handleViewing)
		handler.handlers[RouteUserViews] = http.HandlerFunc(handler.handleUserViews)
		handler.handlers[RouteViews] = http.HandlerFunc(handler.handleViews)
		handler.handlers[RouteVirtualFolders] = http.HandlerFunc(handler.handleVirtualFolders)
		handler.handlers[RouteSelectableMediaFolders] = http.HandlerFunc(handler.handleSelectableMediaFolders)
		handler.handlers[RouteItems] = http.HandlerFunc(handler.handleItems)
		handler.handlers[RouteUserItems] = http.HandlerFunc(handler.handleUserItems)
		handler.handlers[RouteLatestItems] = http.HandlerFunc(handler.handleLatestItems)
		handler.handlers[RouteUserLatestItems] = http.HandlerFunc(handler.handleUserLatestItems)
		handler.handlers[RouteItem] = http.HandlerFunc(handler.handleItem)
		handler.handlers[RouteUserItem] = http.HandlerFunc(handler.handleUserItem)
		handler.handlers[RouteSeasons] = http.HandlerFunc(handler.handleSeasons)
		handler.handlers[RouteEpisodes] = http.HandlerFunc(handler.handleEpisodes)
		handler.handlers[RouteSearchHints] = http.HandlerFunc(handler.handleSearchHints)
		handler.handlers[RouteUserSearchHints] = http.HandlerFunc(handler.handleUserSearchHints)
		handler.handlers[RouteItemsFilters] = http.HandlerFunc(handler.handleItemsFilters)
		handler.handlers[RouteItemsFilters2] = http.HandlerFunc(handler.handleItemsFilters2)
		handler.handlers[RouteSuggestions] = http.HandlerFunc(handler.handleSuggestions)
		handler.handlers[RouteSimilarItems] = http.HandlerFunc(handler.handleSimilarItems)
		handler.handlers[RouteSimilarMovies] = http.HandlerFunc(handler.handleSimilarItems)
		handler.handlers[RouteSimilarShows] = http.HandlerFunc(handler.handleSimilarItems)
		handler.handlers[RouteGenres] = http.HandlerFunc(handler.handleGenres)
		handler.handlers[RouteGenre] = http.HandlerFunc(handler.handleGenre)
		handler.handlers[RoutePersons] = http.HandlerFunc(handler.handlePersons)
		handler.handlers[RoutePerson] = http.HandlerFunc(handler.handlePerson)
		handler.handlers[RouteStudios] = http.HandlerFunc(handler.handleStudios)
		handler.handlers[RouteArtists] = http.HandlerFunc(handler.handleEmptyCatalogDomain)
		handler.handlers[RouteUpcomingShows] = http.HandlerFunc(handler.handleUpcomingShows)
		handler.handlers[RouteMovieRecommendations] = http.HandlerFunc(handler.handleMovieRecommendations)
		handler.handlers[RouteMediaSegments] = http.HandlerFunc(handler.handleMediaSegments)
		handler.handlers[RouteTrickplayImage] = http.HandlerFunc(handler.handleTrickplayImage)
		handler.handlers[RouteTrickplayImageHead] = http.HandlerFunc(handler.handleTrickplayImage)
		handler.handlers[RouteThemeMedia] = http.HandlerFunc(handler.handleThemeMedia)
		handler.handlers[RouteThemeSongs] = http.HandlerFunc(handler.handleThemeSongs)
		handler.handlers[RouteSpecialFeatures] = http.HandlerFunc(handler.handleSpecialFeatures)
		handler.handlers[RouteIntros] = http.HandlerFunc(handler.handleIntros)
		handler.handlers[RouteLocalTrailers] = http.HandlerFunc(handler.handleLocalTrailers)
		handler.handlers[RouteLegacyThemeMedia] = http.HandlerFunc(handler.handleThemeMedia)
		handler.handlers[RouteLegacyThemeSongs] = http.HandlerFunc(handler.handleThemeSongs)
		handler.handlers[RouteLegacySpecialFeatures] = http.HandlerFunc(handler.handleSpecialFeatures)
		handler.handlers[RouteLegacyIntros] = http.HandlerFunc(handler.handleIntros)
		handler.handlers[RouteLegacyLocalTrailers] = http.HandlerFunc(handler.handleLocalTrailers)
		handler.handlers[RouteImageInfos] = http.HandlerFunc(handler.handleImageInfos)
	}
	if serverReady && handler.authentication != nil && handler.catalog != nil && handler.artwork != nil {
		handler.handlers[RouteImage] = http.HandlerFunc(handler.handleImage)
		handler.handlers[RouteImageHead] = http.HandlerFunc(handler.handleImage)
		handler.handlers[RouteIndexedImage] = http.HandlerFunc(handler.handleIndexedImage)
		handler.handlers[RouteIndexedImageHead] = http.HandlerFunc(handler.handleIndexedImage)
	}
	if serverReady && handler.authentication != nil && handler.catalog != nil && handler.playSessions != nil {
		handler.handlers[RoutePlaybackInfo] = http.HandlerFunc(handler.handlePlaybackInfo)
		handler.handlers[RoutePlaybackInfoPost] = http.HandlerFunc(handler.handlePlaybackInfo)
		handler.handlers[RouteUserPlaybackInfo] = http.HandlerFunc(handler.handlePlaybackInfo)
		handler.handlers[RouteUserPlaybackInfoPost] = http.HandlerFunc(handler.handlePlaybackInfo)
		handler.handlers[RouteStream] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteStreamHead] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteContainerStream] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteContainerStreamHead] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteMasterPlaylist] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteMasterPlaylistHead] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteMainPlaylist] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteMainPlaylistHead] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteHLSSegment] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteHLSSegmentHead] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteLegacyHLSSegment] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteLegacyHLSSegmentHead] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteSubtitleStream] = http.HandlerFunc(handler.handleSubtitleStream)
		handler.handlers[RouteSubtitleStreamHead] = http.HandlerFunc(handler.handleSubtitleStream)
		handler.handlers[RouteSubtitleStreamAt] = http.HandlerFunc(handler.handleSubtitleStream)
		handler.handlers[RouteSubtitleStreamAtHead] = http.HandlerFunc(handler.handleSubtitleStream)
		handler.handlers[RouteItemDownload] = http.HandlerFunc(handler.handleDownload)
		handler.handlers[RouteItemDownloadHead] = http.HandlerFunc(handler.handleDownload)
	}
	if serverReady && handler.authentication != nil && handler.catalog != nil && handler.watchstate != nil {
		handler.handlers[RoutePlayedItem] = http.HandlerFunc(handler.handlePlayedItem)
		handler.handlers[RoutePlayedItemDelete] = http.HandlerFunc(handler.handlePlayedItem)
		handler.handlers[RouteUserPlayedItem] = http.HandlerFunc(handler.handleUserPlayedItem)
		handler.handlers[RouteUserPlayedItemDelete] = http.HandlerFunc(handler.handleUserPlayedItem)
		handler.handlers[RouteUserData] = http.HandlerFunc(handler.handleUserData)
		handler.handlers[RouteUserDataUpdate] = http.HandlerFunc(handler.handleUserData)
		handler.handlers[RouteLegacyUserData] = http.HandlerFunc(handler.handleLegacyUserData)
		handler.handlers[RouteLegacyUserDataUpdate] = http.HandlerFunc(handler.handleLegacyUserData)
		handler.handlers[RouteFavoriteItem] = http.HandlerFunc(handler.handleFavoriteItem)
		handler.handlers[RouteFavoriteItemDelete] = http.HandlerFunc(handler.handleFavoriteItem)
		handler.handlers[RouteLegacyFavoriteItem] = http.HandlerFunc(handler.handleLegacyFavoriteItem)
		handler.handlers[RouteLegacyFavoriteItemDelete] = http.HandlerFunc(handler.handleLegacyFavoriteItem)
		handler.handlers[RouteResumeItems] = http.HandlerFunc(handler.handleResumeItems)
		handler.handlers[RouteUserResumeItems] = http.HandlerFunc(handler.handleUserResumeItems)
		handler.handlers[RouteNextUp] = http.HandlerFunc(handler.handleNextUp)
	}
	if serverReady && handler.authentication != nil && handler.catalog != nil && handler.watchstate != nil && handler.playSessions != nil {
		handler.handlers[RoutePlaying] = http.HandlerFunc(handler.handlePlaying)
		handler.handlers[RoutePlayingProgress] = http.HandlerFunc(handler.handlePlayingProgress)
		handler.handlers[RoutePlayingStopped] = http.HandlerFunc(handler.handlePlayingStopped)
		handler.handlers[RoutePlayingPing] = http.HandlerFunc(handler.handlePlayingPing)
	}
}

// ServeHTTP dispatches only exact compatibility routes at the root or below
// one lowercase /emby prefix. It deliberately does not use ServeMux so invalid
// methods and non-canonical paths cannot trigger redirects or implicit HEAD.
func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if handler == nil || !validCompatRequestPath(request) {
		http.NotFound(response, request)
		return
	}

	normalizedPath := NormalizeEmbyPath(request.URL.Path)
	if request.Method == http.MethodOptions {
		handler.serveCompatPreflight(response, request, normalizedPath)
		return
	}
	setCompatCORSHeaders(response, request)
	for _, binding := range handler.routes[request.Method] {
		pathValues, ok := matchRoutePath(binding.definition, normalizedPath)
		if !ok {
			continue
		}

		dispatchedRequest := request
		if normalizedPath != request.URL.Path {
			dispatchedRequest = request.Clone(request.Context())
			dispatchedRequest.URL.Path = normalizedPath
			dispatchedRequest.URL.RawPath = ""
			if strings.HasPrefix(dispatchedRequest.RequestURI, "/emby/") {
				dispatchedRequest.RequestURI = dispatchedRequest.RequestURI[len("/emby"):]
			}
		}
		if binding.definition.Route == RouteLogout {
			dispatchedRequest = withAuthenticatedPolicyExemption(dispatchedRequest)
		}
		for name, value := range pathValues {
			dispatchedRequest.SetPathValue(name, value)
		}
		binding.implementation.ServeHTTP(response, dispatchedRequest)
		return
	}
	http.NotFound(response, request)
}

func (handler *Handler) serveCompatPreflight(response http.ResponseWriter, request *http.Request, normalizedPath string) {
	if request.Header.Get("Origin") == "" || len(request.Header.Get("Origin")) > maximumCompatAuthorizationHeaderBytes {
		http.NotFound(response, request)
		return
	}
	requestedMethod := strings.ToUpper(strings.TrimSpace(request.Header.Get("Access-Control-Request-Method")))
	if requestedMethod == "" || !handler.hasCompatRoute(requestedMethod, normalizedPath) || !validCompatPreflightHeaders(request.Header.Get("Access-Control-Request-Headers")) {
		http.NotFound(response, request)
		return
	}
	setCompatCORSHeaders(response, request)
	response.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Cache-Control, Content-Type, Range, X-Emby-Authorization, X-MediaBrowser-Authorization, X-Emby-Token, X-MediaBrowser-Token")
	response.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, DELETE, OPTIONS")
	response.Header().Set("Access-Control-Max-Age", "600")
	if strings.EqualFold(strings.TrimSpace(request.Header.Get("Access-Control-Request-Private-Network")), "true") {
		response.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) hasCompatRoute(method, normalizedPath string) bool {
	for _, binding := range handler.routes[method] {
		if _, ok := matchRoutePath(binding.definition, normalizedPath); ok {
			return true
		}
	}
	return false
}

func setCompatCORSHeaders(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Origin") == "" {
		return
	}
	response.Header().Set("Access-Control-Allow-Origin", "*")
	response.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Length, Content-Range, ETag, Location")
}

func validCompatPreflightHeaders(value string) bool {
	if len(value) > maximumCompatAuthorizationHeaderBytes {
		return false
	}
	for _, header := range strings.Split(value, ",") {
		switch strings.ToLower(strings.TrimSpace(header)) {
		case "":
		case "accept", "authorization", "cache-control", "content-type", "range",
			"x-emby-authorization", "x-mediabrowser-authorization",
			"x-emby-token", "x-mediabrowser-token":
		default:
			return false
		}
	}
	return true
}

type routeBinding struct {
	definition     RouteSpec
	implementation http.Handler
}

func validCompatRequestPath(request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.Opaque != "" {
		return false
	}
	path := request.URL.Path
	if path == "" || path[0] != '/' || len(path) > maximumCompatPathBytes ||
		request.URL.RawPath != "" || strings.Contains(path, `\`) || strings.Contains(path, "//") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	if request.RequestURI != "" {
		requestPath, _, _ := strings.Cut(request.RequestURI, "?")
		if requestPath != request.URL.EscapedPath() || len(requestPath) > maximumCompatPathBytes {
			return false
		}
	}
	return true
}

func Routes() []RouteSpec {
	result := make([]RouteSpec, len(routeDefinitions))
	copy(result, routeDefinitions)
	return result
}

// NormalizeEmbyPath strips exactly one leading lowercase /emby segment.
func NormalizeEmbyPath(requestPath string) string {
	if requestPath == "/emby" {
		return "/"
	}
	if strings.HasPrefix(requestPath, "/emby/") {
		return requestPath[len("/emby"):]
	}
	return requestPath
}

// IsReservedPath recognizes the compatibility namespaces, including malformed
// paths inside them. The outer router can therefore fail closed before the SPA
// without treating malformed selectors as valid routes.
func IsReservedPath(requestPath string) bool {
	if len(requestPath) > 1 && requestPath[0] == '/' &&
		!strings.Contains(requestPath, `\`) && !strings.Contains(requestPath, "//") &&
		!strings.Contains(requestPath, "/./") && !strings.Contains(requestPath, "/../") &&
		!strings.HasSuffix(requestPath, "/.") && !strings.HasSuffix(requestPath, "/..") {
		segment := requestPath[1:]
		if separator := strings.IndexByte(segment, '/'); separator >= 0 {
			segment = segment[:separator]
		}
		return isCompatRootSegment(segment)
	}

	// Resolve separators only for namespace ownership. The compatibility handler
	// still rejects every non-canonical form, but this keeps the native ServeMux
	// from cleaning one into a compatibility route and issuing a redirect.
	segments := strings.Split(strings.ReplaceAll(requestPath, `\`, "/"), "/")
	resolved := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
			continue
		}
		if len(resolved) == 0 && isCompatRootSegment(segment) {
			return true
		}
		resolved = append(resolved, segment)
	}
	return len(resolved) > 0 && isCompatRootSegment(resolved[0])
}

func isCompatRootSegment(segment string) bool {
	if strings.EqualFold(segment, "emby") {
		return true
	}
	for _, reserved := range compatRootSegments {
		if strings.EqualFold(segment, reserved) {
			return true
		}
	}
	return false
}

var compatRootSegments = []string{
	"System", "QuickConnect", "Users", "Sessions", "Plugins", "Packages",
	"Branding", "DisplayPreferences", "UserViews", "UserItems", "Library", "Items",
	"Playback", "Shows", "Movies", "Genres", "Persons", "Studios", "Artists", "MediaSegments",
	"Search", "SyncPlay", "Videos", "UserPlayedItems", "UserFavoriteItems", "UserImage", "ClientLog", "socket",
}

const maximumCompatPathBytes = 1024

func matchRoutePath(definition RouteSpec, requestPath string) (map[string]string, bool) {
	if requestPath == "" || requestPath[0] != '/' || len(requestPath) > maximumCompatPathBytes ||
		(strings.HasSuffix(requestPath, "/") && requestPath != "/") {
		return nil, false
	}
	patternSegments := strings.Split(strings.TrimPrefix(definition.Pattern, "/"), "/")
	pathSegments := strings.Split(strings.TrimPrefix(requestPath, "/"), "/")
	if len(patternSegments) != len(pathSegments) {
		return nil, false
	}
	var values map[string]string
	for index, patternSegment := range patternSegments {
		pathSegment := pathSegments[index]
		if pathSegment == "" {
			return nil, false
		}
		if patternSegment == "Stream.{format}" {
			separator := strings.LastIndexByte(pathSegment, '.')
			if separator <= 0 || !strings.EqualFold(pathSegment[:separator], "Stream") || pathSegment[separator+1:] != "vtt" {
				return nil, false
			}
			if values == nil {
				values = make(map[string]string, 1)
			}
			values["format"] = "vtt"
			continue
		}
		if patternSegment == "stream.{container}" {
			container := strings.TrimPrefix(pathSegment, "stream.")
			if !validContainer(container) || pathSegment != "stream."+container {
				return nil, false
			}
			if values == nil {
				values = make(map[string]string, 1)
			}
			values["container"] = container
			continue
		}
		if patternSegment == "{index}.jpg" && isTrickplayRoute(definition.Route) {
			indexValue := strings.TrimSuffix(pathSegment, ".jpg")
			if indexValue == pathSegment || !validRouteValue(definition.Route, "index", indexValue) {
				return nil, false
			}
			if values == nil {
				values = make(map[string]string, 4)
			}
			values["index"] = indexValue
			continue
		}
		if patternSegment == "{segmentId}.{container}" {
			separator := strings.LastIndexByte(pathSegment, '.')
			if separator <= 0 || separator == len(pathSegment)-1 {
				return nil, false
			}
			segmentID, container := pathSegment[:separator], pathSegment[separator+1:]
			if !validCapabilityPathSelector(segmentID) || !validContainer(container) {
				return nil, false
			}
			if values == nil {
				values = make(map[string]string, 4)
			}
			values["segmentId"] = segmentID
			values["container"] = container
			continue
		}
		if strings.HasPrefix(patternSegment, "{") && strings.HasSuffix(patternSegment, "}") {
			name := patternSegment[1 : len(patternSegment)-1]
			if !validRouteValue(definition.Route, name, pathSegment) {
				return nil, false
			}
			if values == nil {
				values = make(map[string]string, 4)
			}
			values[name] = pathSegment
			continue
		}
		if !strings.EqualFold(patternSegment, pathSegment) {
			return nil, false
		}
	}
	return values, true
}

func validRouteValue(route Route, name, value string) bool {
	switch name {
	case "displayPreferencesId":
		return validDisplayPreferenceID(value)
	case "id":
		return validCompatUUID(value)
	case "userId", "itemId", "seriesId", "mediaSourceId":
		return validCompatUUID(value)
	case "subtitleIndex":
		parsed, err := strconv.Atoi(value)
		return err == nil && parsed >= 0 && parsed <= maximumCompatibilitySubtitleIndex && strconv.Itoa(parsed) == value
	case "startPositionTicks":
		parsed, err := strconv.ParseInt(value, 10, 64)
		return err == nil && parsed >= 0 && parsed <= 7*24*60*60*TicksPerSecond && strconv.FormatInt(parsed, 10) == value
	case "playlistId":
		return validCapabilityPathSelector(value)
	case "width":
		parsed, err := strconv.Atoi(value)
		return isTrickplayRoute(route) && err == nil && parsed >= minimumTrickplayWidth && parsed <= maximumTrickplayWidth && strconv.Itoa(parsed) == value
	case "name", "genreName":
		return validCatalogPathName(value)
	case "type":
		return supportedCompatImageType(value)
	case "index":
		if isTrickplayRoute(route) {
			parsed, err := strconv.Atoi(value)
			return err == nil && parsed >= 0 && parsed <= maximumTrickplayIndex && strconv.Itoa(parsed) == value
		}
		return value == "0"
	default:
		return false
	}
}

func isTrickplayRoute(route Route) bool {
	return route == RouteTrickplayImage || route == RouteTrickplayImageHead
}

func validCatalogPathName(value string) bool {
	if len(value) == 0 || len(value) > MaximumQueryValueBytes || !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validCapabilityPathSelector(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validDisplayPreferenceID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validContainer(container string) bool {
	if len(container) == 0 || len(container) > 16 {
		return false
	}
	for _, character := range container {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}
