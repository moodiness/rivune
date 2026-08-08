package jellyfin

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

type Route string

const (
	RoutePublicSystemInfo        Route = "public-system-info"
	RouteSystemPing              Route = "system-ping"
	RouteSystemEndpoint          Route = "system-endpoint"
	RouteQuickConnectEnabled     Route = "quick-connect-enabled"
	RouteAuthenticateByName      Route = "authenticate-by-name"
	RoutePublicUsers             Route = "public-users"
	RouteSystemInfo              Route = "system-info"
	RouteCurrentUser             Route = "current-user"
	RouteUser                    Route = "user"
	RouteLogout                  Route = "logout"
	RouteSessionCapabilitiesFull Route = "session-capabilities-full"
	RouteSyncPlayList            Route = "sync-play-list"
	RoutePlaybackBitrateTest     Route = "playback-bitrate-test"
	RoutePlugins                 Route = "plugins"
	RoutePackages                Route = "packages"
	RouteBrandingConfiguration   Route = "branding-configuration"
	RouteBrandingSplashscreen    Route = "branding-splashscreen"
	RouteDisplayPreferences      Route = "display-preferences"
	RouteUserViews               Route = "user-views"
	RouteViews                   Route = "views"
	RouteVirtualFolders          Route = "virtual-folders"
	RouteSelectableMediaFolders  Route = "selectable-media-folders"
	RouteItems                   Route = "items"
	RouteUserItems               Route = "user-items"
	RouteLatestItems             Route = "latest-items"
	RouteUserLatestItems         Route = "user-latest-items"
	RouteItem                    Route = "item"
	RouteUserItem                Route = "user-item"
	RouteSeasons                 Route = "seasons"
	RouteEpisodes                Route = "episodes"
	RouteSearchHints             Route = "search-hints"
	RouteUserSearchHints         Route = "user-search-hints"
	RouteImage                   Route = "image"
	RouteImageHead               Route = "image-head"
	RouteIndexedImage            Route = "indexed-image"
	RouteIndexedImageHead        Route = "indexed-image-head"
	RoutePlaybackInfo            Route = "playback-info"
	RoutePlaybackInfoPost        Route = "playback-info-post"
	RouteStream                  Route = "stream"
	RouteStreamHead              Route = "stream-head"
	RouteContainerStream         Route = "container-stream"
	RouteContainerStreamHead     Route = "container-stream-head"
	RoutePlaying                 Route = "playing"
	RoutePlayingProgress         Route = "playing-progress"
	RoutePlayingStopped          Route = "playing-stopped"
	RoutePlayingPing             Route = "playing-ping"
	RoutePlayedItem              Route = "played-item"
	RoutePlayedItemDelete        Route = "played-item-delete"
	RouteUserPlayedItem          Route = "user-played-item"
	RouteUserPlayedItemDelete    Route = "user-played-item-delete"
	RouteResumeItems             Route = "resume-items"
	RouteUserResumeItems         Route = "user-resume-items"
	RouteNextUp                  Route = "next-up"
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
	RuntimeVersion string
}

var routeDefinitions = []RouteSpec{
	{RoutePublicSystemInfo, http.MethodGet, "/System/Info/Public"},
	{RouteSystemPing, http.MethodGet, "/System/Ping"},
	{RouteSystemEndpoint, http.MethodGet, "/System/Endpoint"},
	{RouteQuickConnectEnabled, http.MethodGet, "/QuickConnect/Enabled"},
	{RouteAuthenticateByName, http.MethodPost, "/Users/AuthenticateByName"},
	{RoutePublicUsers, http.MethodGet, "/Users/Public"},
	{RouteSystemInfo, http.MethodGet, "/System/Info"},
	{RouteCurrentUser, http.MethodGet, "/Users/Me"},
	{RouteUser, http.MethodGet, "/Users/{id}"},
	{RouteLogout, http.MethodPost, "/Sessions/Logout"},
	{RouteSessionCapabilitiesFull, http.MethodPost, "/Sessions/Capabilities/Full"},
	{RouteSyncPlayList, http.MethodGet, "/SyncPlay/List"},
	{RoutePlaybackBitrateTest, http.MethodGet, "/Playback/BitrateTest"},
	{RoutePlugins, http.MethodGet, "/Plugins"},
	{RoutePackages, http.MethodGet, "/Packages"},
	{RouteBrandingConfiguration, http.MethodGet, "/Branding/Configuration"},
	{RouteBrandingSplashscreen, http.MethodGet, "/Branding/Splashscreen"},
	{RouteDisplayPreferences, http.MethodGet, "/DisplayPreferences/{id}"},
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
	{RouteImage, http.MethodGet, "/Items/{id}/Images/{type}"},
	{RouteImageHead, http.MethodHead, "/Items/{id}/Images/{type}"},
	{RouteIndexedImage, http.MethodGet, "/Items/{id}/Images/{type}/{index}"},
	{RouteIndexedImageHead, http.MethodHead, "/Items/{id}/Images/{type}/{index}"},
	{RoutePlaybackInfo, http.MethodGet, "/Items/{id}/PlaybackInfo"},
	{RoutePlaybackInfoPost, http.MethodPost, "/Items/{id}/PlaybackInfo"},
	{RouteStream, http.MethodGet, "/Videos/{id}/stream"},
	{RouteStreamHead, http.MethodHead, "/Videos/{id}/stream"},
	{RouteContainerStream, http.MethodGet, "/Videos/{id}/stream.{container}"},
	{RouteContainerStreamHead, http.MethodHead, "/Videos/{id}/stream.{container}"},
	{RoutePlaying, http.MethodPost, "/Sessions/Playing"},
	{RoutePlayingProgress, http.MethodPost, "/Sessions/Playing/Progress"},
	{RoutePlayingStopped, http.MethodPost, "/Sessions/Playing/Stopped"},
	{RoutePlayingPing, http.MethodPost, "/Sessions/Playing/Ping"},
	{RoutePlayedItem, http.MethodPost, "/Users/{userId}/PlayedItems/{itemId}"},
	{RoutePlayedItemDelete, http.MethodDelete, "/Users/{userId}/PlayedItems/{itemId}"},
	{RouteUserPlayedItem, http.MethodPost, "/UserPlayedItems/{itemId}"},
	{RouteUserPlayedItemDelete, http.MethodDelete, "/UserPlayedItems/{itemId}"},
	{RouteResumeItems, http.MethodGet, "/Users/{id}/Items/Resume"},
	{RouteUserResumeItems, http.MethodGet, "/UserItems/Resume"},
	{RouteNextUp, http.MethodGet, "/Shows/NextUp"},
}

type Dependencies struct {
	ServerInfo     ServerInfo
	Authentication Authentication
	Catalog        CatalogReader
	Collections    CollectionReader
	Artwork        ArtworkDelivery
	Playback       PlaybackDelivery
	Watchstate     Watchstate
	Logger         *slog.Logger

	// Handlers overrides built-ins for focused tests. Unknown or nil overrides
	// are rejected; built-ins are installed only when their dependencies exist.
	Handlers map[Route]http.Handler
}

type Handler struct {
	serverInfo     ServerInfo
	authentication Authentication
	catalog        CatalogReader
	collections    CollectionReader
	artwork        ArtworkDelivery
	playback       PlaybackDelivery
	watchstate     Watchstate
	playSessions   *playSessionRegistry
	logger         *slog.Logger
	handlers       map[Route]http.Handler
	routes         map[string][]routeBinding
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
		serverInfo:     dependencies.ServerInfo,
		authentication: dependencies.Authentication,
		catalog:        dependencies.Catalog,
		collections:    dependencies.Collections,
		artwork:        dependencies.Artwork,
		playback:       dependencies.Playback,
		watchstate:     dependencies.Watchstate,
		logger:         dependencies.Logger,
		handlers:       make(map[Route]http.Handler, len(routeDefinitions)),
		routes:         make(map[string][]routeBinding, 4),
	}
	if _, serverReady := handler.publicSystemInfo(); serverReady && handler.authentication != nil && handler.catalog != nil && handler.playback != nil {
		handler.playSessions = newPlaySessionRegistry(handler.playback)
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
			implementation: traceRoute(handler.logger, definition, implementation),
		})
	}
	return handler, nil
}
func (handler *Handler) installBuiltInHandlers() {
	_, serverReady := handler.publicSystemInfo()
	if serverReady {
		handler.handlers[RoutePublicSystemInfo] = http.HandlerFunc(handler.handlePublicSystemInfo)
		handler.handlers[RouteSystemPing] = http.HandlerFunc(handler.handleSystemPing)
		handler.handlers[RouteSystemEndpoint] = http.HandlerFunc(handler.handleSystemEndpoint)
		handler.handlers[RouteQuickConnectEnabled] = http.HandlerFunc(handler.handleQuickConnectEnabled)
		handler.handlers[RoutePublicUsers] = http.HandlerFunc(handler.handlePublicUsers)
		handler.handlers[RouteBrandingConfiguration] = http.HandlerFunc(handler.handleBrandingConfiguration)
		handler.handlers[RouteBrandingSplashscreen] = http.HandlerFunc(handler.handleBrandingSplashscreen)
	}
	if serverReady && handler.authentication != nil {
		handler.handlers[RouteAuthenticateByName] = http.HandlerFunc(handler.handleAuthenticateByName)
		handler.handlers[RouteSystemInfo] = http.HandlerFunc(handler.handleSystemInfo)
		handler.handlers[RouteCurrentUser] = http.HandlerFunc(handler.handleCurrentUser)
		handler.handlers[RouteUser] = http.HandlerFunc(handler.handleUser)
		handler.handlers[RouteLogout] = http.HandlerFunc(handler.handleLogout)
		handler.handlers[RouteSessionCapabilitiesFull] = http.HandlerFunc(handler.handleSessionCapabilitiesFull)
		handler.handlers[RouteSyncPlayList] = http.HandlerFunc(handler.handleSyncPlayList)
		handler.handlers[RoutePlaybackBitrateTest] = http.HandlerFunc(handler.handlePlaybackBitrateTest)
		handler.handlers[RoutePlugins] = http.HandlerFunc(handler.handlePlugins)
		handler.handlers[RoutePackages] = http.HandlerFunc(handler.handlePackages)
		handler.handlers[RouteDisplayPreferences] = http.HandlerFunc(handler.handleDisplayPreferences)
	}
	if serverReady && handler.authentication != nil && handler.catalog != nil {
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
		handler.handlers[RouteStream] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteStreamHead] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteContainerStream] = http.HandlerFunc(handler.handleStream)
		handler.handlers[RouteContainerStreamHead] = http.HandlerFunc(handler.handleStream)
	}
	if serverReady && handler.authentication != nil && handler.catalog != nil && handler.watchstate != nil {
		handler.handlers[RoutePlayedItem] = http.HandlerFunc(handler.handlePlayedItem)
		handler.handlers[RoutePlayedItemDelete] = http.HandlerFunc(handler.handlePlayedItem)
		handler.handlers[RouteUserPlayedItem] = http.HandlerFunc(handler.handleUserPlayedItem)
		handler.handlers[RouteUserPlayedItemDelete] = http.HandlerFunc(handler.handleUserPlayedItem)
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
	"Playback", "Shows", "Search", "SyncPlay", "Videos", "UserPlayedItems",
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
	case "id":
		if route == RouteDisplayPreferences {
			return validDisplayPreferenceID(value)
		}
		return validCompatUUID(value)
	case "userId", "itemId", "seriesId":
		return validCompatUUID(value)
	case "type":
		return value == "Primary" || value == "Backdrop" || value == "Thumb"
	case "index":
		return value == "0"
	default:
		return false
	}
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
